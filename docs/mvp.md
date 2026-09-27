# llm-bench MVP 実装仕様(v0.1)

> **この文書が MVP の唯一の実装仕様である。**`docs/architecture.md`(v1.6/v1.7)と `docs/optimization.md` は将来構想の履歴として残すが、MVP ではこの文書が優先する。実装順序は `docs/plan.md`。
> `AGENTS.md`(規約)・`docs/design.md`(ドメイン要件)は引き続き有効。矛盾する場合はこの文書と `AGENTS.md` を優先する。
>
> 決定の出所: 2026-09-27 のオペレータ判断(ChatGPT との MVP ノート)。

---

## 0. MVP の定義(一文)

```
GitHub Issue を Controller が poll し、GPU Sandbox を安全に確保して、
benchmark なら Sandbox 内の llmbench を実行し、optimization なら外部 Agent/DSH に Sandbox を貸し出す。
計測と experiments/ の生成は Sandbox 内の llmbench が担当し、成果物は Git/PR に残す。
```

2 本の経路を end-to-end で動かすことが MVP の完了条件。**benchmark 経路は `cmd/llmbench/e2e_test.go` で固定してある**(クラスタが要る協働相手だけを差し替え、本物の CLI・harness・push・compare を通す)。**optimization 経路は未実装**: Agent の sandbox 操作(§8.1)はあるが、Controller は `kind: optimize` を明示的に拒否したまま。

| 経路 | 流れ |
|---|---|
| benchmark | `llmbench:benchmark` Issue → Sandbox → `llmbench benchmark` → `experiments/` → ブランチ push → PR |
| optimization | `llmbench:optimize` Issue → Agent/DSH + 使い捨て GPU Sandbox → 反復 benchmark → 最終 runtime 変更 + `experiments/` → PR |

---

## 1. 決定事項(ロック済み)

| 項目 | 決定 |
|---|---|
| Controller の実行形態 | クラスタ常駐 **Deployment(1 replica)** |
| GPU 同時実行数 | **1**。`coordination.k8s.io/v1` の Lease 1 本で global ownership を持つ |
| 既存 GPU workload の扱い | GitOps manifest の pause / restore(マージ待ち)。既存実装を流用 |
| ジョブ状態の持ち方 | **メモリ + GPU Lease の annotation(永続 phase)+ Issue ラベル(人間向けのミラー)**。ConfigMap store と leader election は**使わない** |
| HTTP API | MVP の経路に置かない。既存 `serve` は freeze(削除は後続 PR) |
| Agent / DSH | **operator が常駐させる独立 Deployment**(llm-bench は作らない)。Controller は **ACP(`pods/exec` 経由)で job ごとに 1 セッション作成**して task を渡し、以後は干渉しない。完了は sandbox 内の記録 |
| Agent → Sandbox | `llmbench sandbox job exec|push|pull|ls <job id>`(Agent Sandbox SDK の port-forward transport)。ssh も対話 shell も実装しない(exec は shell 無しの argv)。job id は Controller が task で渡す |
| GitHub 認証 | **GitHub App**(private key は Controller のみ)。Sandbox には短命 installation token のみ渡す |
| 計測の実行者 | **Sandbox 内の `llmbench`**。runtime 起動・workload・metrics・`experiments/` 書き出し・commit/push まで |
| 結果の形式 | 本仕様 §4.3 の案を採用。**実データを見てから調整する**(互換性は保証しない) |
| 旧仕様との互換性 | **保たない**。今回の目的に最適化する |
| visual 公開(v1.6 preview/adopt/sitebuild/Pages/review) | freeze。MVP では使わない |

---

## 2. 構成

```
GitHub (Issue = 要求 / PR = 成果物)
   ▲ │ poll, comment, PR
   │ ▼
llm-bench Controller (Deployment, outbound only, メモリ + Issue ラベル)
   │  Lease / SandboxClaim / GitOps pause-restore
   │
   ├─ Agent Pod (GPU 不要, 既存 DSH deployment) ── 共通 session PVC
   │        │  llmbench sandbox exec|cp (port-forward)
   │        ▼
   └─ GPU Sandbox Pod (使い捨て, SandboxClaim 経由)
            ├─ llmbench benchmark (計測の実行主体)
            ├─ runtime (llama.cpp / FreeToken / ...)
            ├─ metrics collectors (harness / runtime / nvidia)
            └─ git commit / push (短命 installation token)
```

- **Agent Pod と GPU Sandbox Pod は別 Pod**。GPU node 障害・GPU 割当失敗・Sandbox 消失で Agent session を失わない。
- **Sandbox lifecycle と Agent session lifecycle は独立**。Sandbox が死んだら Controller が replacement を作り、Agent に execution backend を rebind する。
- Controller は **outbound のみ**。webhook も public endpoint も作らない。

---

## 3. Job(要求の受け取り方)

### 3.1 Issue が正本

- Issue Form(`.github/ISSUE_TEMPLATE/benchmark.yml` / `optimize.yml`)で本文に **fenced YAML ブロック**を 1 つ生成する。Controller は本文中の最初の ` ```yaml ` ブロックだけを JobSpec として読む。
- ラベルは種別と、人間が読むための粗い状態を持つ。** durable な phase は GPU Lease の annotation が正本**(§6.1)。
  - 種別: `llmbench:benchmark` / `llmbench:optimize`
  - 状態(ミラー): `llmbench:claimed`(処理中)/ `llmbench:done` / `llmbench:failed`
- 人手の介入は Issue の close とラベル付けのみ。**run は明示的な要求(`llmbench:*` ラベルの付いた Issue)でのみ開始する**。PR や Issue の編集だけでは開始しない。
- 種別ラベルと状態ラベルは Controller が起動時に作成する。状態ラベルは Lease の phase の写しなので、人間が手で外しても次の resync で戻る。
- 成果物は PR。Issue へは**リンクコメント 1 件**だけ返す(計測値の貼り付けはしない)。

### 3.2 JobSpec

```yaml
kind: benchmark                     # benchmark | optimize
model:
  id: qwen38-27b                    # operator のモデル置き場から解決(パスは書けない)
runtime:
  engine: llamacpp                  # adapter 名(operator の allowlist と一致すること)
  image: ghcr.io/example/llama.cpp:cuda13-b4xxx   # operator allowlist と一致すること
  args: ["-ngl", "99", "-c", "4096"]   # user-tunable な flag だけ。adapter 所有の flag は拒否される(§3.4)
  ready: {port: 8080, path: /health, timeout_seconds: 300}
workload:
  cases:
    - name: short
      prompt: prompts/short.txt     # リポジトリ相対 or インラインの `prompt_text`
      max_tokens: 256
      repeats: 3
  concurrency: 1                    # MVP は 1 のみ(並列計測は未実装。2 以上は拒否)
  sampling: {temperature: 0, seed: 1}
metrics:
  collectors: [harness, runtime, nvidia]
source:                             # kind: optimize では必須
  repo: nomanoma121/llama.cpp
  ref: main
budget:                             # kind: optimize のみ(任意)
  max_rounds: 20
output:
  dir: experiments/qwen38-27b
```

- **特権は JobSpec に書けない**。どの GPU / target を取るか、pause 対象、image とモデルの allowlist、ネームスペースは **operator 設定のみ**が決める。JobSpec は allowlist から選ぶだけ。
- 検証は `internal/job` の 1 実装を CLI と Controller が共有する。unknown field は拒否(fail-closed)。

### 3.3 job id と出力先

- job id: `YYYY-MM-DD-issue<NNN>`(同日再実行は `-r2`, `-r3`)
- 出力先: `<output.dir>/<job id>/`

### 3.4 `runtime.args` の権限分離

`image` と `model` を operator allowlist で縛っても、`runtime.args` に任意の flag を書ければ **adapter が管理する設定を上書きできる**(別の model path を指す、listen アドレスを変える、metrics / log の出力を消す、config ファイルを差し替える)。したがって:

- **adapter が所有する flag**(model path、listen host/port、metrics endpoint、log 出力、config ファイル、runtime の識別情報)は **JobSpec から設定できない**。runtime adapter がその一覧を宣言し、`internal/job` の `Constraints.ReservedArgs` として CLI と Controller の両方で**拒否**する(unknown field と同じ fail-closed)。
- 予約リストは **モデルの取得元を変える flag をすべて含む**。llama.cpp は `-m/--model` だけでなく `-mu/--model-url`、`-dr/--docker-repo`、`-hf/-hfr/--hf-repo`、`-hff/--hf-file`、`--lora*`、`--mmproj` も予約する。FreeToken は `--model/--model-path/--model-source`、`--dummy-weight`、`--gpu` も予約する。**upstream がモデル選択 flag を増やしたら adapter の予約リストも更新する**(adapter の契約の一部)。
- JobSpec の `args` に書けるのは **tuning 系**(`-ngl`、`-c`、batch、thread 数、MoE cache、attention backend など)だけ。
- adapter が組み立てる argv は「adapter 所有の必須引数 + spec の tuning 引数」の順で、必須引数を spec が上書きできないことを保証する。

---

## 4. Workload と Metrics(新規。旧仕様と互換性なし)

### 4.1 実行モデル

1. Sandbox Pod の image には **runtime binary** が入っている(llama.cpp / FreeToken など)。
2. `llmbench benchmark` が runtime を **localhost で起動**し、`ready` を満たすまで待つ。
3. case ごとに prompt を投入し、`repeats` 回計測する。
4. 計測の前後で collector を回し、runtime を停止する。
5. 結果を `experiments/` へ書き、`--push` なら commit / push する。

runtime adapter が持つのは次の 3 点だけ:

- 起動 argv の組み立て(JobSpec の `args` + `engine` 固有の既定)
- prompt 投入の仕方(HTTP API のエンドポイント形)
- runtime metrics の取得方法(`/metrics` の Prometheus text、またはログの解析)

対象はまず `llamacpp` と `freetoken`。runtime が metrics を出さなくても harness 計測だけで動く。

### 4.2 metric(単位はここで固定)

| source | metric |
|---|---|
| `harness` | `ttft_ms`, `prefill_tok_per_s`, `decode_tok_per_s`, `latency_ms`(p50/p95), `total_ms`, `requests`, `tokens_in`, `tokens_out` |
| `runtime` | `cache_hit_ratio`, `kv_cache_used_mib`, `mtp_acceptance`(あれば) |
| `nvidia` | `vram_used_mib`, `vram_total_mib`, `gpu_util_percent` |

- **値を出した source を必ず記録する**。`harness` と `external_gpu`(nvidia-smi)は harness が直接観測した値、`runtime` は runtime の自己申告である。
- **collector は workload 実行中に周期的にサンプルする**(既定 500ms)。GPU の VRAM ピークや util 平均は、ケース終了後の1回の読み取りでは意味を持たない(ピークを取り逃し、idle 値を平均してしまう)。集約は「VRAM = 周期サンプルの最大」「util = 周期サンプルの平均」。
- **startup の 1 回の読み取りでは required collector を満たさない**: 要求された collector は **workload 実行中に最低 1 回**サンプルしなければ充足とみなさない(workload より前に取った値は workload について何も言えないため、足りなければ invalid)。
- **runtime metrics は要約 metric にしない**。llama.cpp の counter はプロセス生存期間の累積、FreeToken の throughput は sliding window であり、ケース後の1回の読み取りを「その case の値」とラベル付けすると意味が変わる。runtime の値は **`series.jsonl` のサンプル(時刻と case ラベル付き)としてのみ**記録し、Agent が推移を読む。
- **測定対象そのものを凍結する**: `result.json` の `inputs` に、解決済みの `model_path`・operator が pin した `model_digest`・**各 case のプロンプト実バイトの digest**・**`workload_digest`**(cases / sampling / collector 構成)を記録する(JobSpec には model id とプロンプトのパスしか無いため、別の weights や別のプロンプトファイルを指しても「同じ入力」に見えてしまう)。**プロンプトは測定前に一度だけ読み、その同一バイトを digest と実際のリクエストの両方に使う**(ファイルが途中で変わっても digest と送信内容がずれない)。**`model_digest` は operator が pin する**: pin が無い実行は入力 identity を凍結できないため `measurement_valid=false` になる。
- **環境(GPU の機種とドライバ)は collector の設定に依存せず取得する**。nvidia collector を要求していないジョブでも環境 identity は記録される(取得に失敗しても invalid にはしない。要求していた場合だけ gap になる)。
- **identity・validity・環境情報は harness が持つ**(runtime にも candidate にも書かせない)。JobSpec・runtime image・model・prompt・収集 collector の digest を結果に焼き込む。
- `measurement_valid` は「測定チャネルが信頼できるか」のみを表す。collector の欠測、想定外の他プロセス、GPU の throttling などで false になる。runtime が自己申告を出さない場合も `runtime` を要求する JobSpec では invalid になる(要求した source が取れない場合は valid にしない)。
- `llmbench compare` は `valid` / `comparable` を**事実として**返す。採否は返さない。

### 4.3 出力(`<output.dir>/<job id>/`)

| ファイル | 内容 | digest |
|---|---|---|
| `jobspec.yaml` | 受理した JobSpec の凍結コピー(operator 適用後の解決済み値も含む) | `jobspec_digest`(意味的 identity)+ `jobspec_file_digest`(ファイルのバイト) |
| `result.json` | canonical な結果。metrics / series の要約 / environment / runtime / collectors / validity / inputs + 下記の digest | `result_digest` |
| `series.jsonl` | per-request / per-step の生サンプル(1 行 1 サンプル、計測順に追記) | `series_digest` |
| `README.md` | 人間向け要約(自動生成) | × |
| `raw/` | runtime ログ、nvidia-smi 生出力など | × |

- `result.json` は v1.7 設計の measurement スキーマ(`metric` / `series` / `collectors` / `environment` / `runtime`)を流用する。`internal/measurement` が既に実装・テスト済み。
- **digest は `result.json` の中に閉じる**(外部ファイルの digest を各自で計算し直さなくても identity が決まる):
  - `jobspec_digest` = canonical な JobSpec(`internal/job.Digest()`)の SHA-256。operator が解決した値も含めた実効 spec の digest。
  - `series_digest` = `series.jsonl` の全バイトの SHA-256。**計測順を固定**し、後から並べ替えられないようにする。
  - `jobspec_file_digest` = `jobspec.yaml` のバイト列の SHA-256。意味的な digest(`jobspec_digest`)は整形に依存しないが、こちらは隣に置かれた実ファイルと結果を結びつける。
  - `result_digest` = `jobspec_digest` / `jobspec_file_digest` / `series_digest` を**含む** canonical な `result.json` payload(`result_digest` 自身を除く)の SHA-256。
  - したがって **`jobspec.yaml` や `series.jsonl` を書き換えると `series_digest` → `result_digest` が変わり、identity が変わる**(「digest 対象」の矛盾はここで閉じる)。
- **identity は `result_digest`**。baseline と candidate の比較、PR の記述、後続の追跡はこの digest で行う。
- **検証はディレクトリ単位で行う**(`VerifyResultDir` 相当): `result.json` 自身の digest だけでなく、`jobspec.yaml` / `series.jsonl` の実バイトを記録された digest と照合する。`result.json` だけを検証すると、`series.jsonl` を差し替えても valid のままになってしまう。
- `inputs`(解決済みモデルとプロンプトの digest)も `result_digest` に含まれる。
- **trust level を記録する**: driver の隔離(候補が書けない場所からの実行と `Driver.ContentDigest` の実行時検証)は F で行うため、それまでの結果には `provenance.trust_level`(例 `unverified-driver`)を書く。後から「この結果はどの程度信頼できるか」が変に読み替えられないようにする。
- `raw/` と `README.md` は digest の対象外(後から再生成・追記できる)。

---

## 5. `llmbench` CLI(Sandbox 内で実行する)

| コマンド | 内容 |
|---|---|
| `llmbench benchmark --job <file\|-> [--out <dir>] [--push] [--json]` | runtime 起動 → workload → 計測 → `experiments/` 書き出し。`--push` でブランチ作成 + commit + push |
| `llmbench compare <baseline dir> <candidate dir> [--kind model\|runtime] [--json]` | **事実のみ**: metric ごとの delta、`measurement_valid`、`comparable`(+ 理由の全列挙)。`accept`/`reject`/`good`/`bad` は返さない |
| `llmbench job validate <file\|->` | JobSpec 検証(Controller と同一コード) |
| `llmbench job init --kind <benchmark\|optimize>` | Issue に貼る JobSpec の雛形を出力 |
| `llmbench sandbox job exec <job id> -- <argv...>` | 実行先 Sandbox でコマンド実行(rebind は自動) |
| `llmbench sandbox job push\|pull <job id> ...` | 実行先 Sandbox とのファイル転送 |
| `llmbench sandbox job ls <job id> [path]` | 実行先 Sandbox の一覧 |
| `llmbench job done --job <job id> --status complete\|failed [--branch B --commit C]` | Agent の完了を sandbox 内のファイルに記録 |
| `llmbench version` | |

- exit code: `0` 成功(invalid な measurement でも成功)/ `2` 入力不正 / `3` 競合(Lease が取れない)/ `10` 結果を作れなかった(runtime が起動しない・serving にならない・中断)/ `11` timeout(readiness)
- `--json` は機械可読の 1 オブジェクトを stdout に出す(Agent はこれを読む)。ログは stderr。
- MVP で使わない既存コマンド(`submit` / `status` / `serve` / `review` / `adopt` / `metrics` など)は freeze し、MVP が動いた後に削除する。

---

## 6. Controller

### 6.1 ループ

Deployment 1 replica。in-flight job はメモリに 1 つだけ持つ。**durable な phase は GPU Lease の annotation** に書く(§6.2)。

```
poll(open な `llmbench:benchmark|optimize` Issue で、状態ラベルが無いもの)
  → validate(JobSpec)                        失敗: コメント + `llmbench:failed`
  → **Lease acquire**(global GPU ownership)   失敗＝他が処理中なので次の poll で
      holderIdentity = job id、annotation = JobSpec digest + Issue 番号
  → **claim は Lease 取得後に行う**: Issue に `llmbench:claimed` を付け、
      付けられたか read-back で確認(Lease がジョブ取得の排他なので、
      Lease を持っている Pod 以外は claim しない)
  → GitOps pause(PR → マージ待ち → Synced 確認)
  → SandboxClaim ensure Ready(label: `llmbench.io/job-id=<job id>`)
  → benchmark: Sandbox 内で `llmbench benchmark --push` を exec し、branch/SHA を受領
    optimize:  Agent の完了を SandboxClaim の annotation で観測(§8)
  → PR 作成(push 済みブランチから)
  → SandboxClaim delete
  → GitOps restore(PR → マージ待ち)
  → Lease release(phase = released を書いてから削除。ここで job 完了)
  → `llmbench:done` + コメント(PR リンク)
```

### 6.2 durable な phase(Lease annotation)

`claimed` / `done` / `failed` だけでは、exec の前か後か・push 済みか・pause PR が merge 済みか・PR を作ったか、を区別できない。区別できないまま recovery すると「invoking からは再実行しない」という規則を守れない。そこで **GPU Lease の annotation を 1 ジョブ分の write-ahead 記録**として使う(新しい Kubernetes リソースも ConfigMap store も作らない。Lease は元々 GPU 所有権のために取得する):

| annotation | 意味 |
|---|---|
| `llmbench.io/phase` | `acquired` → `paused` → `sandbox_ready` → `executing` → `executed` → `pr_open` → `claim_deleted` → `restored` → `released` |
| `llmbench.io/job-id` / `llmbench.io/issue` | 対象 |
| `llmbench.io/jobspec-digest` | 受理した JobSpec(再実行要求の同一性判定) |
| `llmbench.io/branch` / `llmbench.io/commit` | push 済みの成果(`executed` 以降) |
| `llmbench.io/pause-pr` | pause / restore の PR 番号(マージ確認待ちの対象) |
| `llmbench.io/attempt` | Sandbox replacement の世代(§6.4) |

- 各 phase は**外部効果の前に**書く(意図を先に記録し、効果は冪等にする)。
- `executing` で落ちた場合、その外の効果(exec)を再実行したかどうか分からないので **再実行しない**。restore / release だけを行い、失敗として記録する。
- Issue の状態ラベル(`claimed` / `done` / `failed`)は **人間向けのミラー**であり、phase の写しを resync で貼り直すだけ。

### 6.3 規律

- **すべての段は再実行可能**。外部効果の前に phase を書き、効果自体は冪等にする。
- **restore は必ず通す**。benchmark が失敗しても、timeout でも、Controller が再起動しても restore と release に到達する。restore が終わるまで job を完了扱いにしない。
- **同じ job を再実行しない**。recovery は phase で判断する: `executed` / `opening_pr` / `pr_open` は **branch/commit が durable なので PR 作成だけを再実行**して cleanup へ進む(PR 作成は冪等)。`executing` は「測定が完了したか不明」なので**再実行せず** failure として記録して cleanup する。それ以前の phase は未測定なので attempt を破棄して failure にする。
- **restore が converge するまで Lease を手放さない**。`Sandbox.Delete` がまだ終わっていない / `Restore` が `ErrNotConverged`(PR 未マージ・Argo 未同期)の間は phase も Lease も保持したまま recovery が再試行する。Lease を先に離すと、まだ pause されたままの GPU を次の job が借りてしまう。
- **outcome は測定直後の annotate で durable にする**(phase=`executed` と同じ書き込み)。測定後の crash で「成功した job を recovery が failed と解釈する」事故を防ぐ。
- **終了ラベルを先に付け、`claimed` を後に外す**。terminal ラベルの書き込みに失敗したら **Lease は保持**したまま recovery が再試行する(状態ラベルが 1 つも無いまま release すると、同じ Issue が pending に見えて二重実行される)。
- **Lease の renewal は lease を保持している間ずっと動く**(run() のスコープに縛らない)。restore PR のマージ待ちのように recovery pass をまたぐ待機では、pass ごとに renewal を止めると lease が失効する。recovery の各 pass は最初に `Renew` して ownership を確認してから外部効果を行う。
- **state ラベルは相互に異なること**(operator 検証)。`done == claimed` のような設定だと、終了時に付けたラベルを自分で外してしまい、同じ Issue が pending に戻って二重実行される。
- **renew 間隔は lease duration から導出する**(既定 = duration/3)。renew 間隔が lease 期間以上だと、測定中に失効して別 instance に引き継がれる。設定で明示する場合は duration 未満でなければならない(起動時に fail)。`lease.duration_seconds` は 60 以上。
- **ラベルの mirror は Lease release より先に書く**(crash しても released record の recovery が label だけ resync できる)。
- **Lease を失ったらジョブを止める**(fencing): renew が失敗した時点で実行中の context を cancel する。lease を失ったプロセスは外部効果(測定・push)を続けてはならない。
- **runtime 子プロセスに出版 credential を渡さない**: `llmbench benchmark` は runtime を起動する際、環境変数から `LLMBENCH_GIT_TOKEN` を除く(runtime は候補のコードであり、環境をログに出すと write token が生の出力に漏れる)。
- **outcome を cleanup の前に durable に書く**(`llmbench.io/outcome`)。cleanup は `deleting_claim` / `restoring` / `releasing` の間にも crash しうるので、その経路で recovery が「成功/失敗」を再判定してはならない(成功した job が failed ラベルになる事故を防ぐ)。終了ラベルは outcome だけが決め、`claimed` と反対側の終了ラベルは必ず外す。
- **released な record(Holder 空)は recovery の対象外**。annotation は履歴として残るが、完了済み job を復活させない。
- **`gitops` は必須**。備え付けの GPU を pause できなければ Controller は動かせないので、設定検証の段階で落とす(Lease を取った後に初めて失敗する事態を避ける)。
- **Lease は release 完了まで renew する**(restore PR のマージ待ちが lease 期間を超えても、生きている Controller の lease を失効させない)。
- **Lease は instance 単位で fencing する**。holder は Pod 名などの instance-unique な値にし、**別 instance の live lease は引き継がない**(引き継ぐのは失効した lease のみ)。同じ holder の中断ジョブだけを recovery が再開する。
- Sandbox が死んだ場合は **attempt を破棄して replacement で最初から再実行**する(benchmark。`attempt` annotation を進める)。optimize では Agent session を維持したまま rebind する(§8)。

### 6.4 recovery

- startup と定期(既定 60s)に、未完了 job について **Lease(phase)/ SandboxClaim / Sandbox status / GitOps pause state / Agent Pod / Issue ラベル**を照合し、phase の次の段から再開する。
- メモリが消えても復帰できること: 「いまどの段か」は **Lease の phase** から決まる。ConfigMap store も leader election も作らない。
- Lease は失効・再取得できてしまうので、**Lease を持っていても annotation の `job-id` が自分の解いている job と一致しない場合は何もしない**(rollout 中に旧 Pod が新しい job に手を出さないための確認)。
- controller-runtime ベースの reconciler は作らない。小さなループで足りる。

---

## 7. GitHub 連携

- **GitHub App** を使う。private key は Controller の Secret のみ。JWT(RS256)→ installation token(キャッシュし、期限前に更新)を発行する。対象 repo は 1 つ固定。
- Controller の用途: Issue の poll / ラベル / コメント / PR 作成。
- Sandbox に渡すのは **短命 installation token のみ**(単一 repo の `contents: write`)。private key は絶対に渡さない。Agent Pod にも渡さない。
- **push は fail-closed**(§5 の `--push`): 同名ブランチが remote に既にあれば拒否、remote の **default branch を実際に問い合わせて**拒否(name の deny-list だけに頼らない)、commit は指定パス配下のみ(事前に stage 済みのファイルを巻き込まない)、force は決してしない。
- **Sandbox 用 token は用途を絞る**: Controller 自身の token とは別に、**対象 repo 1 つ + `contents: write` だけ**の installation token を**ジョブごとに新規に mint**して環境変数で渡す(GitHub は body が空だと installation の全 repo・全権限を渡してしまう)。token の寿命は 1 時間なので、それを超える実行(長時間の optimize)では push が失敗する — その場合は Controller 側 push か token 再取得が必要(MVP では未実装として明示)。
- **installation token は branch-scoped ではない**。optimize の Sandbox で Agent が自由にコマンドを実行できる以上、token を読んで任意ブランチに push できる。したがって **default branch はリポジトリ側で保護する**: PR 必須、force push 禁止、admin bypass 無効、GitHub App が直接 push できない設定(ruleset で bypass リストに入れない)。これは MVP の必須要件であり、Sandbox に token を渡す前提条件である。
- outbound のみ。webhook、public endpoint は作らない。
- Git 操作(`commit` / `push`)は **Sandbox 内の `llmbench`** が行う。PR 作成は Controller が行う(GitHub API の credential を Controller に閉じ込める)。

---

## 8. Agent / DSH(optimize)

Agent は **DSH(DeepSeek Harness)** が担う。DSH は **operator が常駐させる独立した Deployment** であり、llm-bench はそれを作らないし、サブプロセスでも、ワンショット実行でもない。

### 8.1 セッションの作成(Controller は作るだけ、以後は干渉しない)

```
optimize job:
  pause → SandboxClaim 作成(label: llmbench.io/job-id=<job>)
  → Controller が harness に ACP で **1 セッション作成**し、task を1回だけ渡す
  → 以後 Controller は干渉しない(harness が session を所有する)
       ・session/update をログに流す
       ・session/request_permission に**ポリシーで自動応答**(既定は全部許可)
  → 完了 = **sandbox 内の完了ファイル**(Agent が `llmbench job done` で書く)。
       ACP の prompt settlement は補助情報
  → session/close → PR 作成 → SandboxClaim 削除 → restore → lease release
```

- **transport は ACP のみ**。DSH に REST API は無く、`dsh --profile acp` が **stdio 上の ACP v1** を喋る。Controller は harness Pod に **`pods/exec`** で接続してその stream を使う(新しい Pod は作らない)。
- 使うメソッド: `initialize` / `session/new`(cwd は harness Pod 側の作業ディレクトリ)/ `session/prompt` / `session/close`、必要なら `session/list` + `session/resume`。`session/update` を購読し、`session/request_permission` に答える(`internal/acp`)。
- task には **job id / model / runtime image / workload / budget / sandbox の触り方(`llmbench sandbox job exec|push|pull <job-id>`)/ 完了記録の書き方(`llmbench job done`)** を含める。Agent は job id を教えられるので discovery に頼らない。
- **セッションが作成された後の所有権は DSH 側**。Controller がやるのは、ACP の接続を保持して更新と権限要求に応えること、そして終了を待つことだけ。ACP は接続を切ると harness が agent を drain するため、接続はセッションの寿命と一致させる。
- **再起動時**: 完了ファイルがあれば PR 作成まで進める。無ければこの job は失敗として記録する(**セッションを再開して task を送り直すことはしない** — 同じ Run を再実行しない規律と同じ)。

### 8.2 Agent の sandbox アクセス

- 各 SandboxClaim は `llmbench.io/job-id=<job id>` と `app.kubernetes.io/managed-by=llmbench` を持つ。`llmbench sandbox job exec|push|pull|ls <job id>` は、Kubernetes API から**その label の current claim を毎回 discovery** して port-forward transport を張る。**rebind は discovery の結果が変わること**そのもの。Controller から Agent へ接続情報を push しない。
- **同じ job-id の claim が 2 つ見つかった場合はエラー**(replacement の途中)。
- Agent の ServiceAccount には、そのネームスペースの `sandboxclaims` の `get` / `list` / `watch`、pod の `get` / `list`、port-forward だけを与える(**claim への書き込み権限は与えない**。RBAC は update を annotation に限定できないため)。Helm の `mvp.agentServiceAccount.{namespace,name}` で bind する。
- Controller 側は harness Pod の `pods/exec` を必要とする(Helm は `mvp.agent.*` が設定されたときだけその Role を作る)。

### 8.3 完了の記録

- Agent は最後に `llmbench job done --job <job id> --status complete|failed [--branch B --commit C]` を実行し、**sandbox 内のファイル**(`/workspace/.llmbench-agent-result.json`)に記録する。
- Controller は sandbox からそれを読み、**同じ契約を検証する**(既知の status、`complete` なら branch と commit)。読み取りは **64 KiB 上限**、**ファイルが無い = 未完了**、それ以外の失敗(port-forward 断・sandboxd 死)はエラー。
- claim の annotation ではなくファイルにする理由: Agent に claim の書き込み権限を与えずに済み、記録が成果物と同じ場所に残る。

### 8.4 設定(operator 所有)

| values | 内容 |
|---|---|
| `agent.namespace` / `agent.pod_selector` | 常駐する harness Deployment の Pod を選ぶ |
| `agent.container` | Pod に複数コンテナがあるときだけ |
| `agent.exec` | ACP を提供する argv(既定 `["dsh","--profile","acp"]`)|
| `agent.cwd` | `session/new` の cwd(harness Pod の fs 上の絶対パス。既定 `/workspace`)|

- **Agent のモデルは DSH 側の設定**(profile)で決まる。llm-bench は指定しない(変えたいときは harness 側を変える)。
- 権限ポリシーは**全部許可**。安全性はサンドボックス隔離と RBAC で担保する(lease・manifest には触れない)。
- conversation / agent loop / tool history / session resume は **DSH が持つ**。llm-bench は conversation store を作らない。
- 途中の失敗 attempt は DSH の session history に残す。llm-bench は optimization log を保存しない。
- Agent の採否判断は Agent 自身。`llmbench compare` は事実のみを返す。甘い採用は最終的に人間が PR レビューで否決する。
- visual benchmark の human-in-the-loop(「この画像で良い?」)は MVP の scope 外。vision-capable Agent が render → screenshot → 評価まで自分で行う。

---

## 9. Helm(`charts/llmbench`)

| values | 内容 |
|---|---|
| `controller` | Deployment(1 replica)、resources、ログレベル |
| `serviceAccount` / `rbac` | **ネームスペースごとの Role**(lease / sandbox / Argo CD Application / workload)と Agent 用 Role。ClusterRole は使わない |
| `github` | App id / installation id / private key Secret 名 / repo |
| `gpuLease` | Lease 名とネームスペース |
| `gitops` | 対象 repo、manifest パス、pause 値、restore 値、待ち時間 |
| `sandbox` | SandboxTemplate / WarmPool 名、image allowlist、GPU リソース要求 |
| `models` | モデル id → 置き場 のマッピング(operator 権限) |
| `agent` | namespace / pod_selector / container / exec(ACP の argv)/ cwd |
| `dsh` | **Agent の ServiceAccount(namespace + name)**(sandbox アクセス用の Role を bind)。harness 本体の manifest は operator 側 |
| `runtimeImages` | allowlist |

---

## 10. MVP で実装しないもの

MeasurementWindow / ActiveRunID / PromotionPolicy による自動採否 / OptimizationSession 台帳 / metrics diff の高度機能 / Agent 用 HTTP API / ConfigMap run store / leader election / 複数 GPU job concurrency / 独自 conversation store / 独自 session DB / Discord 通知 / Issue 上の visual approval / optimization_logs / 全 candidate の永続化 / HA・multi-replica / SSH executor

---

## 11. 既存コードの扱い

| 扱い | 対象 |
|---|---|
| **流用** | `internal/provenance`(digest) / `internal/measurement`(evidence スキーマ・validity・canonical・seal) / `internal/operator`(allowlist) / `internal/gitops`(pause/restore) / `internal/sandbox`(claim + port-forward exec) / `internal/kube`(Lease)/ `internal/hook` + recovery の規律 |
| **置き換え** | `internal/experiment`(visual recipe)→ `internal/job`(JobSpec) / `internal/runner` の visual 前提部分 → Sandbox 内 `llmbench benchmark`(runtime adapter)/ `internal/run` の重い状態機械 → 薄い in-flight job(メモリ + Issue ラベル。write-ahead と mandatory restore の規律は維持) |
| **freeze** | `internal/httpapi` + `serve` / `internal/preview` / `internal/adopt` / `internal/sitebuild` / `internal/review` / `internal/discord` / `internal/pages`(削除済み)/ `.github/workflows/pages.yml` |
| **削除(後続 PR)** | 上記 freeze 対象と、visual 専用になった runner 経路。MVP が動いてから消す |

---

## 12. 実装順序

`docs/plan.md` の「4. MVP」節に PR 単位で書く。概略:

1. `internal/job`(JobSpec の parse / validate)+ `llmbench job validate|init` + Issue Form
2. `internal/runtime`(adapter: llamacpp / freetoken)+ `llmbench benchmark`(計測 + `experiments/` 出力)
3. `llmbench compare`(事実のみ)
4. `internal/githubapp`(App 認証 + installation token)+ Issue polling + ブランチからの PR 作成
5. Controller 薄版(Deployment、Lease、pause/restore、SandboxClaim、exec、recovery)
6. Helm chart(controller + operator 設定)
7. Agent / DSH の bind / rebind + `llmbench sandbox exec|cp`
8. 実データを見て結果形式を調整 → freeze 対象の削除
