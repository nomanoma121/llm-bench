#!/usr/bin/env python3
"""本体の分割GGUF + MTPヘッドGGUF -> MTP入りの単一GGUF を1パスで書き出す。
   元ファイルは読み取りのみ。numpy不要（テンソルデータはバイトコピー）。"""
import struct, sys, glob, os

SZ = {0:1,1:1,2:2,3:2,4:4,5:4,6:4,7:1,10:8,11:8,12:8}

def rd(f, fmt): return struct.unpack(fmt, f.read(struct.calcsize(fmt)))
def rstr(f):
    (n,) = rd(f,'<Q')
    if n > (1<<24): raise ValueError('bad string length %d' % n)
    return f.read(n)
def skipval(f, t):
    if t in SZ: f.seek(SZ[t],1)
    elif t == 8:
        (n,) = rd(f,'<Q'); f.seek(n,1)
    elif t == 9:
        (at,) = rd(f,'<I'); (n,) = rd(f,'<Q')
        if at in SZ: f.seek(SZ[at]*n,1)
        else:
            for _ in range(n): skipval(f,at)
    else: raise ValueError('unknown kv type %d' % t)

def read_gguf(path):
    """-> (kvs, tensors, data_start, alignment)
       kvs: [(name_bytes, type, raw_value_bytes)]
       tensors: [(name_bytes, dims, ttype, offset)]"""
    f = open(path,'rb')
    magic = f.read(4)
    assert magic == b'GGUF', (path, magic)
    (ver,) = rd(f,'<I'); (nt,) = rd(f,'<Q'); (nkv,) = rd(f,'<Q')
    kvs = []
    for _ in range(nkv):
        name = rstr(f); (t,) = rd(f,'<I')
        p0 = f.tell(); skipval(f,t); p1 = f.tell()
        f.seek(p0); raw = f.read(p1-p0)
        kvs.append((name, t, raw))
    tensors = []
    for _ in range(nt):
        name = rstr(f); (nd,) = rd(f,'<I'); dims = rd(f,'<%dQ' % nd)
        (tt,) = rd(f,'<I'); (off,) = rd(f,'<Q')
        tensors.append((name, list(dims), tt, off))
    align = 32
    for name,t,raw in kvs:
        if name == b'general.alignment' and t == 4:
            align = struct.unpack('<I', raw)[0]
    pos = f.tell()
    data_start = (pos + align - 1) // align * align
    f.close()
    return kvs, tensors, data_start, align

# --- ggml 型 -> (block bytes, block elems) ---
TY = {0:(4,1),1:(2,1),2:(18,32),3:(20,32),6:(22,32),7:(24,32),8:(34,32),9:(40,32),
      10:(84,256),11:(110,256),12:(144,256),13:(176,256),14:(210,256),15:(292,256),
      16:(66,256),17:(74,256),18:(98,256),19:(50,256),20:(18,32),21:(110,256),
      22:(82,256),23:(136,256),24:(1,1),25:(2,1),26:(4,1),27:(8,1),28:(8,1),
      29:(56,256),30:(2,1),34:(54,256),35:(66,256)}
def nbytes(dims, tt):
    ne = 1
    for d in dims: ne *= d
    bs, blk = TY[tt]
    assert ne % blk == 0, (dims, tt)
    return ne // blk * bs

def wstr(b): return struct.pack('<Q', len(b)) + b

def main():
    trunk_glob, head_path, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
    shards = sorted(glob.glob(trunk_glob))
    assert shards, 'no trunk shards'
    print('本体シャード %d 個, ヘッド %s' % (len(shards), os.path.basename(head_path)))

    # 本体: シャード1のKVを土台にする
    base_kvs, _, _, align = read_gguf(shards[0])
    # 本体の全テンソル (シャード順)
    trunk = []   # (name, dims, tt, src_path, src_off, data_start)
    for sp in shards:
        kvs, ts, ds, al = read_gguf(sp)
        for (name, dims, tt, off) in ts:
            trunk.append((name, dims, tt, sp, off, ds))
    print('  本体テンソル %d 本' % len(trunk))

    # ヘッド: blk.48.* だけ採用 (token_embd/output は本体のものを使う)
    hkvs, hts, hds, hal = read_gguf(head_path)
    head = [(n,d,t,head_path,o,hds) for (n,d,t,o) in hts if n.startswith(b'blk.48.')]
    print('  ヘッドから採用 %d 本 (除外 %d 本)' % (len(head), len(hts)-len(head)))
    nextn = [n for n,_,_,_,_,_ in head if b'nextn' in n]
    print('  nextn テンソル %d 本' % len(nextn))

    allt = trunk + head

    # --- KV の加工 ---
    hkv = {n:(t,r) for (n,t,r) in hkvs}
    out_kvs = []
    seen = set()
    for (name, t, raw) in base_kvs:
        if name.startswith(b'split.'):
            continue                                  # 単一ファイル化
        if name == b'qwen4exp.block_count':
            assert t == 4
            raw = struct.pack('<I', 49)                # 48 -> 49
        elif t == 9 and name in hkv and hkv[name][0] == 9:
            # per-layer 配列: 本体48要素 / ヘッド49要素 ならヘッドを採用
            at_a, n_a = struct.unpack('<IQ', raw[:12])
            at_b, n_b = struct.unpack('<IQ', hkv[name][1][:12])
            if at_a == at_b and n_b == n_a + 1:
                print('  配列KVをヘッドの49要素版に差し替え: %s (%d -> %d)'
                      % (name.decode(), n_a, n_b))
                raw = hkv[name][1]
        out_kvs.append((name, t, raw)); seen.add(name)
    if b'qwen4exp.nextn_predict_layers' not in seen:
        out_kvs.append((b'qwen4exp.nextn_predict_layers', 4, struct.pack('<I',1)))
    print('  KV %d -> %d' % (len(base_kvs), len(out_kvs)))

    # --- ヘッダを組み立てて各テンソルのオフセットを決める ---
    hdr = bytearray()
    hdr += b'GGUF' + struct.pack('<I',3) + struct.pack('<Q',len(allt)) + struct.pack('<Q',len(out_kvs))
    for (name,t,raw) in out_kvs:
        hdr += wstr(name) + struct.pack('<I',t) + raw
    # テンソル情報は2パス: まずサイズ確定のためダミーoffsetで組んで長さを測る
    infos = bytearray()
    off = 0
    placed = []
    for (name, dims, tt, sp, soff, sds) in allt:
        sz = nbytes(dims, tt)
        placed.append((name, dims, tt, sp, soff, sds, off, sz))
        off += (sz + align - 1) // align * align
    for (name, dims, tt, sp, soff, sds, o, sz) in placed:
        infos += wstr(name) + struct.pack('<I', len(dims))
        for d in dims: infos += struct.pack('<Q', d)
        infos += struct.pack('<I', tt) + struct.pack('<Q', o)
    head_len = len(hdr) + len(infos)
    data_start = (head_len + align - 1) // align * align
    total = data_start + off
    print('  ヘッダ %d B, データ %.2f GiB, 合計 %.2f GiB' % (head_len, off/2**30, total/2**30))

    # --- 書き出し ---
    with open(out_path,'wb') as w:
        w.write(hdr); w.write(infos)
        w.write(b'\0' * (data_start - head_len))
        cur = {}
        done = 0
        for i,(name, dims, tt, sp, soff, sds, o, sz) in enumerate(placed):
            if sp not in cur: cur[sp] = open(sp,'rb')
            r = cur[sp]; r.seek(sds + soff)
            assert w.tell() == data_start + o, (w.tell(), data_start+o, name)
            left = sz
            while left:
                chunk = r.read(min(left, 1<<24))
                if not chunk: raise IOError('short read %s %s' % (sp, name))
                w.write(chunk); left -= len(chunk)
            pad = (align - (sz % align)) % align
            if pad: w.write(b'\0'*pad)
            done += sz
            if i % 200 == 0 or i == len(placed)-1:
                print('    %5d/%d  %.1f GiB' % (i+1, len(placed), done/2**30), flush=True)
        for r in cur.values(): r.close()
    print('完了: %s (%.2f GiB)' % (out_path, os.path.getsize(out_path)/2**30))

main()
