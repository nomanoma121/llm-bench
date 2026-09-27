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

2 本の経路を end-to-end で動かすことが MVP の完了条件。

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
| ジョブ状態の持ち方 | **メモリ + Issue ラベル**。ConfigMap store と leader election は**使わない**(必要になったら戻す) |
| HTTP API | MVP の経路に置かない。既存 `serve` は freeze(削除は後続 PR) |
| Agent / DSH | **既存デプロイを使う**。llm-bench は Sandbox を貸し出し、bind / rebind とライフサイクルだけを持つ。Helm の option で参照 |
| Agent → Sandbox | `llmbench sandbox exec|cp|shell`(Agent Sandbox SDK の port-forward transport)。ssh は実装しない |
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
- ラベルで種別と状態を持つ。**ラベルがジョブの永続状態**である。
  - 種別: `llmbench:benchmark` / `llmbench:optimize`
  - 状態: `llmbench:claimed`(処理中)/ `llmbench:done` / `llmbench:failed`
- 人手の介入は Issue の close とラベル付けのみ。**run は明示的な要求(`llmbench:*` ラベルの付いた Issue)でのみ開始する**。PR や Issue の編集だけでは開始しない。
- 成果物は PR。Issue へは**リンクコメント 1 件**だけ返す(計測値の貼り付けはしない)。

### 3.2 JobSpec

```yaml
kind: benchmark                     # benchmark | optimize
model:
  id: qwen38-27b                    # operator のモデル置き場から解決(パスは書けない)
runtime:
  engine: llamacpp                  # adapter 名(operator の allowlist と一致すること)
  image: ghcr.io/example/llama.cpp:cuda13-b4xxx   # operator allowlist と一致すること
  args: ["-ngl", "99", "-c", "4096"]
  ready: {port: 8080, path: /health, timeout_seconds: 300}
workload:
  cases:
    - name: short
      prompt: prompts/short.txt     # リポジトリ相対 or インラインの `prompt_text`
      max_tokens: 256
      repeats: 3
  concurrency: 1
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

- **値を出した source を必ず記録する**。`harness` / `nvidia` は harness が直接観測した値、`runtime` は runtime の自己申告である。
- **identity・validity・環境情報は harness が持つ**(runtime にも candidate にも書かせない)。JobSpec・runtime image・model・prompt・収集 collector の digest を結果に焼き込む。
- `measurement_valid` は「測定チャネルが信頼できるか」のみを表す。collector の欠測、想定外の他プロセス、GPU の throttling などで false になる。runtime が自己申告を出さない場合も `runtime` を要求する JobSpec では invalid になる(要求した source が取れない場合は valid にしない)。
- `llmbench compare` は `valid` / `comparable` を**事実として**返す。採否は返さない。

### 4.3 出力(`<output.dir>/<job id>/`)

| ファイル | 内容 | digest 対象 |
|---|---|---|
| `jobspec.yaml` | 受理した JobSpec の凍結コピー(operator 適用後の解決済み値も含む) | ○ |
| `result.json` | canonical な結果。metrics / series の要約 / environment / runtime / collectors / validity + `result_digest` | ○(`result_digest` 自身は除く) |
| `series.jsonl` | per-request / per-step の生サンプル(1 行 1 サンプル) | ○ |
| `README.md` | 人間向け要約(自動生成) | × |
| `raw/` | runtime ログ、nvidia-smi 生出力など | × |

- `result.json` は v1.7 設計の measurement スキーマ(`metric` / `series` / `collectors` / `environment` / `runtime`)を流用する。`internal/measurement` が既に実装・テスト済み。
- **identity は `result_digest`**(canonical payload の SHA-256)。baseline と candidate の比較、PR の記述、後続の追跡はこの digest で行う。
- `raw/` と `README.md` は digest の対象外(後から再生成・追記できる)。

---

## 5. `llmbench` CLI(Sandbox 内で実行する)

| コマンド | 内容 |
|---|---|
| `llmbench benchmark --job <file\|-> [--out <dir>] [--push] [--json]` | runtime 起動 → workload → 計測 → `experiments/` 書き出し。`--push` でブランチ作成 + commit + push |
| `llmbench compare <baseline dir> <candidate dir> [--json]` | **事実のみ**: metric ごとの中央値と delta、`valid`, `comparable`(+ 理由)、prefill 回帰、VRAM 差分。`accept`/`reject`/`good`/`bad` は返さない |
| `llmbench job validate <file\|->` | JobSpec 検証(Controller と同一コード) |
| `llmbench job init --kind <benchmark\|optimize>` | Issue に貼る JobSpec の雛形を出力 |
| `llmbench sandbox exec <job id> -- <argv...>` | 実行先 Sandbox でコマンド実行 |
| `llmbench sandbox cp <job id> <src> <dst>` | 実行先 Sandbox とのファイル転送 |
| `llmbench sandbox shell <job id>` | 対話シェル(人間用) |
| `llmbench version` | |

- exit code: `0` 成功 / `2` 入力不正 / `3` 競合(Lease が取れない)/ `10` job 失敗 / `11` timeout
- `--json` は機械可読の 1 オブジェクトを stdout に出す(Agent はこれを読む)。ログは stderr。
- MVP で使わない既存コマンド(`submit` / `status` / `serve` / `review` / `adopt` / `metrics` など)は freeze し、MVP が動いた後に削除する。

---

## 6. Controller

### 6.1 ループ

Deployment 1 replica。in-flight job はメモリに 1 つだけ持つ。状態は Issue ラベルから再構成できる。

```
poll(open な `llmbench:benchmark|optimize` Issue で、状態ラベルが無いもの)
  → validate(JobSpec)                      失敗: コメント + `llmbench:failed`
  → claim(`llmbench:claimed` を付ける)      失敗＝他が処理中なので skip
  → Lease acquire(global GPU ownership)
  → GitOps pause(PR → マージ待ち → Synced 確認)
  → SandboxClaim ensure Ready
  → bind(optimize のときだけ Agent に sandbox 情報を渡す)
  → benchmark: Sandbox 内で `llmbench benchmark --push` を exec し、branch/SHA を受領
    optimize:  Agent の完了(commit/push)を待つ
  → PR 作成(push 済みブランチから)
  → SandboxClaim delete
  → GitOps restore(PR → マージ待ち)
  → Lease release
  → `llmbench:done` + コメント(PR リンク)
```

### 6.2 規律

- **すべての段は再実行可能**。外部効果の前に「何をしようとしているか」を残す(write-ahead)。
- **restore は必ず通す**。benchmark が失敗しても、timeout でも、Controller が再起動しても restore と release に到達する。restore が終わるまで job を完了扱いにしない。
- **同じ job を再実行しない**。中断(invoking 中の状態)から復帰した場合は再実行せず failure として記録する。
- Sandbox が死んだ場合は **attempt を破棄して replacement で最初から再実行**する(benchmark)。optimize では Agent session を維持したまま rebind する。

### 6.3 recovery

- startup と定期(既定 60s)に、未完了 job について **Lease / SandboxClaim / Sandbox status / GitOps pause state / Agent Pod / Issue ラベル**を照合し、到達可能な段から再開する。
- メモリが消えても復帰できること: 「いまどの段か」は Kubernetes の状態と Issue ラベルから再構成する。ConfigMap store も leader election も作らない。
- controller-runtime ベースの reconciler は作らない。小さなループで足りる。

---

## 7. GitHub 連携

- **GitHub App** を使う。private key は Controller の Secret のみ。JWT(RS256)→ installation token(キャッシュし、期限前に更新)を発行する。対象 repo は 1 つ固定。
- Controller の用途: Issue の poll / ラベル / コメント / PR 作成。
- Sandbox に渡すのは **短命 installation token のみ**(単一 repo の `contents: write`)。private key は絶対に渡さない。Agent Pod にも渡さない。
- outbound のみ。webhook、public endpoint は作らない。
- Git 操作(`commit` / `push`)は **Sandbox 内の `llmbench`** が行う。PR 作成は Controller が行う(GitHub API の credential を Controller に閉じ込める)。

---

## 8. Agent / DSH(optimize)

- llm-bench は **Agent Pod を作らない**。既存の DSH deployment を Helm の option で参照する(session 用 PVC は DSH 側の共通 1 本)。
- Controller の責務は「Sandbox を用意し、Agent に bind する」ことと「Sandbox が置き換わったら rebind する」ことだけ。
- Agent は `llmbench sandbox exec|cp|shell` で Sandbox 内の workspace を編集・実行する(port-forward transport)。ssh は実装しない。
- conversation / agent loop / tool history / session resume は **DSH が持つ**。llm-bench は conversation store を作らない。
- 途中の失敗 attempt は DSH の session history に残す。llm-bench は optimization log を保存しない。
- Agent の採否判断は Agent 自身。`llmbench compare` は事実のみを返す。甘い採用は最終的に人間が PR レビューで否決する。
- visual benchmark の human-in-the-loop(「この画像で良い?」)は MVP の scope 外。vision-capable Agent が render → screenshot → 評価まで自分で行う。

---

## 9. Helm(`charts/llmbench`)

| values | 内容 |
|---|---|
| `controller` | Deployment(1 replica)、resources、ログレベル |
| `serviceAccount` / `rbac` | Lease、SandboxClaim、Sandbox、Pod exec、Secret 参照 |
| `github` | App id / installation id / private key Secret 名 / repo |
| `gpuLease` | Lease 名とネームスペース |
| `gitops` | 対象 repo、manifest パス、pause 値、restore 値、待ち時間 |
| `sandbox` | SandboxTemplate / WarmPool 名、image allowlist、GPU リソース要求 |
| `models` | モデル id → 置き場 のマッピング(operator 権限) |
| `dsh` | enabled / existing deployment 名 / session PVC 名(Agent Pod の manifest 自体は DSH 側) |
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
