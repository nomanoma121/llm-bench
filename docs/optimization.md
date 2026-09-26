# llm-bench 測定・最適化設計(v0.1 / architecture v1.7 の一部)

> この文書は `docs/architecture.md`(v1.7)の一部を成す詳細設計である。**未実装**であり、実装は §14 のフェーズ(A〜H)で段階的に行う。
> 参照: `docs/design.md`(要件)・`docs/architecture.md`(blueprint)・`docs/plan.md`(進捗)。

対象は「Agent が runtime を最適化するループ」と「そのための測定(メトリクス)基盤」。既存の visual benchmark(A/B 人間レビュー)はそのまま残し、**測定は別系統の sealed evidence として扱う**。

---

## 1. 目的とスコープ

- Agent(または人間)が runtime の変更(llama.cpp 等の patch、量子化、起動パラメータ)を提案し、**測定 → 機械判定 → 採否**を回せるようにする
- 測定値は「最適化に必要な数値」(decode ms/step、prefill、VRAM、キャッシュ、内訳)を **harness/信頼できる driver 側で自動収集**し、run ごとに不変な evidence として保存する
- 採用した runtime は**OCI image として公開**し、deployment は digest で pin する
- **人間の visual A/B レビューは最終候補のみ**に置く(内側ループには入れない)

スコープ外(§13): Run 内部の multi-attempt、Window をまたぐ runtime プロセス再利用、Agent への採否権限、runtime 自己申告値を primary objective にすること。

---

## 2. 不変条件の変更(architecture v1.7)

### 2.1 resource ownership

```
旧:
  1 target = 1 active Run
  Run terminal => target resources are restored

新:
  1 target = 1 exclusive resource owner
  owner = standalone Run | MeasurementWindow

  standalone Run:
    terminal => すべての target resource が復元済み(従来どおり)

  window-bound Run:
    run-scoped resource(command hook, SandboxClaim)のみを持ち、
    target が paused のまま terminal になりうる。
    window-scoped resource(TargetLease, GitOps pause/restore)の復元は
    MeasurementWindow の責務であり、WindowProved closed は
    復元と TargetLease 解放が完了するまで到達しない。
```

standalone Run の意味論は**一切変更しない**。

### 2.2 artifact と evidence の分離

```
ArtifactDigest != ""   = sealed visual artifact(output/index.html)が存在する
MetricsDigest  != ""   = sealed measurement evidence(evidence/metrics.json)が存在する
```

- `Run.Kind = visual` の成功条件: `ArtifactDigest` 必須(`MetricsDigest` は任意)
- `Run.Kind = measurement` の成功条件: `MetricsDigest` 必須、`ArtifactDigest` は**作らない**
- preview / review / adopt は visual artifact を要求する(従来どおり)
- 最適化の判定(`decide`)は `MetricsDigest` の存在を条件にする(visual + metrics の run にも使える)

### 2.3 comparability と subject identity の分離

既存 `BenchmarkFingerprint`(visual A/B 用)は**変更しない**。runtime 最適化では runtime の digest が「変えてよいもの」なので、fingerprint に入れると比較不能になる。比較の可否は `compare --kind` が must-equal / may-differ で判定する(§7)。

---

## 3. RunKind

```go
type RunKind string
const (
    RunKindVisual      RunKind = "visual"      // 既定。visual artifact を要求
    RunKindMeasurement RunKind = "measurement" // evidence のみ
)
```

- `Kind` と protocol の付与は**独立**する。`Kind` は operator の profile / 明示リクエストが決め、submit 時に snapshot として固定する。**recipe は protocol 名も Kind も指定できない**(experiment.Config に selector を足さない)
- `MeasurementProtocol` は `visual` にも `measurement` にも付与できる(visual run に所要時間・VRAM の evidence を付けるのは自由。review marker v2 はその両方を扱う)
- 実行実装は分けるが**状態機械は共通**: `ExecutorRouter{visual: VisualExecutor, measurement: MeasurementExecutor}`
- `measurement` は `output/` を作らない(single-file 契約の対象外)。`visual` は従来どおり `output/index.html` 必須
- `ArtifactDigest` は **sealed visual artifact の有無**を表す。`measurement` では空が正常であり、「空 = 未確定」という v1.6 の意味は visual に限定する

---

## 4. Resource ownership と MeasurementWindow

### 4.1 なぜ必要か

`pause/restore` は GitOps PR(人間のマージ)を伴うため 1 run ごとに回すと破綻する(20 ラウンド × 6 run = 120 サイクル)。Window は **pause を保持したまま複数の測定 run を実行**し、PR サイクルを 1 回にまとめる。

### 4.2 所有範囲(最小)

| resource | owner |
|---|---|
| TargetLease | Window |
| GitOps pause/restore hook | Window |
| command hook | Run |
| SandboxClaim | Run |
| runtime プロセス | Run |

SandboxClaim まで Window が持つと「中断した runtime プロセスの掃除」を別途 write-ahead 化する必要があり、状態機械の変更が大きくなる。**まずは pause/restore だけを持ち上げる。**

### 4.3 状態

```go
type WindowPhase string
const (
    WindowPending   WindowPhase = "pending"
    WindowAcquiring WindowPhase = "acquiring"
    WindowOpen      WindowPhase = "open"
    WindowClosing   WindowPhase = "closing"
    WindowClosed    WindowPhase = "closed"
)
type WindowResult string
const (
    WindowResultNone    WindowResult = "none"
    WindowResultSuccess WindowResult = "success"
    WindowResultFailure WindowResult = "failure"
)
```

Window record: `ID / Target / Phase / Result / LeaseState / HookPlan / HookPlanDigest / Hooks / ActiveRunID / MaxDurationDeadline / IdleDeadline / LastActivityAt / Error / StoreVersion / CreatedAt / UpdatedAt`。

`WindowStore` は `RunStore` と同じ契約(CAS による `StoreVersion`、`ListUnfinished`)。

### 4.3b hook 計画の所有分割

hook 計画を 3 契約に分け、それぞれに digest を持たせる。

| 契約 | 対象 | 内容 |
|---|---|---|
| `BuildHookPlan(target, runID)` | standalone Run | 従来どおり `command → gitops → sandbox-claim` |
| `BuildWindowHookPlan(target, windowID)` | Window | **GitOps pause/restore のみ**。ブランチ名は **windowID 由来** |
| `BuildWindowBoundRunHookPlan(target, runID)` | window-bound Run | `command → sandbox-claim`(**GitOps を含めない**) |

- window-bound Run の実効順序は `(Window) GitOps pause → command → SandboxClaim` となり、旧不変条件「command が GitOps より先」の**明示的な例外**になる(Window が pause を持ち上げるため)
- 3 契約すべてで `HookPlanDigest` を検証してから構築する(従来の原則を維持)

### 4.4 owner の一般化

```go
type OwnerRef struct {
    Kind string // "run" | "window"
    ID   string
}
type LeaseStore interface {
    AcquireTargetLease(ctx context.Context, target string, owner OwnerRef) error
    ReleaseTargetLease(ctx context.Context, target string, owner OwnerRef) error
}
```

文字列連結ではなく型で持つ。既存の owner 条件付き削除・NotFound=成功の契約は維持する。

### 4.5 Window のライフサイクル

```
acquiring: TargetLease acquire(write-ahead) → GitOps pause hook acquire → open
open:      1 run のみ ActiveRunID を CAS で取得して実行できる
closing:   ActiveRunID が空になるまで release を開始しない
           → GitOps restore hook release → TargetLease release → closed
```

不変条件:

- `open` 以外では新しい Run slot を取得できない
- `ActiveRunID` は最大 1 件(CAS 必須。worker が複数 `open` を同時に読めるため)
- `closing` に入ったら `open` に戻らない。`closing` 中は新しい Run を開始しない
- external effect は Window でも write-ahead
- release エラー中は `closing` のまま。`closed` にしてはならない
- TargetLease release 完了前に `closed` へ行かない
- standalone Run の意味論は変更しない

### 4.5c v1.6 以前の record との後方互換

- **`Run.Kind == ""` は legacy visual として扱う**(v1.6 record はすべて visual artifact を持つ)。新規 Run は必ず non-empty の `Kind` を保存する
- `LeaseState` は既存 record の値をそのまま維持する。**`not_applicable` は新規 window-bound Run だけ**が持つ
- `WindowID == ""` は従来の standalone Run と同義(新規・legacy とも)

### 4.5b window-bound Run の LeaseState

window-bound Run は TargetLease を所有しないため、`LeaseState` に **`not_applicable`** を追加し、window-bound では acquire/release のどのステップも実行しない(値は submit 時に `not_applicable` で固定)。`releasing` の完了判定・terminal 判定で `LeaseState` を参照してはならない。

### 4.6 window-bound Run の acquire/release

engine の**明示分岐**で実装する(no-op な LeaseStore/HookSource の差し替えは、standalone run まで通す fail-open を作るので禁止)。

```
window-bound Run:
  1. WindowStore から window を読む
  2. phase == open かつ target 一致を確認
     - open だが ActiveRunID が他 run → ErrPending
     - missing / target mismatch / closing / closed → 恒久エラー(ErrPending にしない)
  3. ActiveRunID を CAS で自分にする
  4. run-scoped hook(command / SandboxClaim)を通常どおり acquire
  5. execute
  6. run-scoped hook を通常どおり release(**window-scoped の TargetLease / GitOps restore は触らない**)
  7. ActiveRunID を CAS で空にする
  8. **CAS が成功してからのみ** terminal へ進む(失敗は ErrPending で再試行)
```

### 4.7 recovery

起動時は **Windows → Runs** の順に回復する。

| 観測 | 動作 |
|---|---|
| `ActiveRunID` が unfinished run を指す | その run を recover |
| `ActiveRunID` が terminal run を指す | CAS で slot 解放 |
| `ActiveRunID` が存在しない run を指す | stale slot として解放 |
| `WindowID` を持つ run があるが window が無い | その run は実行しない(恒久エラー) |
| `open` のまま期限超過(`max_window_duration` / `idle_timeout`) | reconciler が `closing` へ落とす |

controller が死んでも reconciler が release → restore に必ず収束する。

---

## 5. Measurement evidence

### 5.1 パスと identity

```
runs/<run-id>/
  input/                 # recipe snapshot(従来)
  output/index.html      # visual payload(visual run のみ)
  evidence/metrics.json  # sealed measurement evidence(measurement run は必須)
  invoke.log
  model-identity.json
```

- **sealed evidence の authoritative writer は harness**。Sandbox には `raw-measurement.json` を書かせ、harness が `PullLimited` で回収 → validate → normalize → canonical JSON → fsync → atomic rename → `MetricsDigest` を計算して Run に保存する
- Executor の戻り型は `Artifacts` ではなく **`ExecutionOutputs{Artifacts, Evidence{Path,Digest}, RuntimeBuildDigest, EnvironmentDigest, WorkloadDigest}`** とし、`ExecCompleted` の保存と同じ CAS で Run へ反映する(§architecture §3.2 の定義が正)。**`Execute` が error を返しても返却済みの sealed Evidence は保存する**
- `MetricsDigest` は「sealed bytes の SHA-256」(canonical JSON を harness が生成する)。**`MetricsDigest != ""` は「evidence が存在する」ことだけを意味し、成功を意味しない**
- 実行が失敗しても、**取得できた evidence は seal してよい**(`measurement_valid` と `invalid_reasons` を持たせる)
- `measurement` run の成功条件は「**valid な evidence が存在する**」こと。validity の判定基準は §5.5 の表が正であり、**correctness 失敗・候補起因 OOM/Xid は validity を落とさない**(valid evidence 上の reject)。invalid/infra 由来の evidence しか無い run を使った `decide` は `inconclusive` を返す(§6.3)
- **`ExecutionResult` の決定規則(measurement)**: valid な測定が得られたら `success`(correctness 失敗や候補起因 OOM は *測定結果* であって Execute の error にしない)。測定チャネル/infra の失敗は `failure` とし、取得済み evidence があれば seal して保存する。これにより「Run の成功」と「promotion の accept」が分離する
- `output/` に evidence を置かない(single-file 契約と衝突する)

### 5.2 スキーマ(versioned)

```json
{
  "schema_version": 1,
  "run_id": "...",
  "kind": "measurement",
  "protocol": {"id": "qwen-flash-longctx-v1", "digest": "..."},
  "environment": {"digest": "...", "gpus": [{"model": "V100-SXM2-32GB", "count": 2}], "driver": "..."},
  "runtime": {"spec_digest": "...", "build_digest": "..."},
  "measurement_valid": true,
  "invalid_reasons": [],
  "metrics": [
    {
      "name": "decode_step_ms",
      "value": 17.71,
      "unit": "ms/step",
      "source": "driver",
      "labels": {"depth": "64k", "draft": "3"},
      "stats": {"count": 385, "mean": 17.8, "median": 17.71, "p90": 18.2, "mad": 0.4},
      "samples": {"count": 385, "downsampled": false}
    }
  ],
  "series": [
    {"name": "prefill_ms_by_depth", "unit": "ms", "points": [[13700, 20.6], [65536, 108.1]], "original_count": 2}
  ],
  "collector": [{"name": "nvidia-smi", "interval_ms": 500, "gaps": 0}]
}
```

- **metric source は必須**: `harness | driver | external_gpu | runtime | profiler`。`PromotionPolicy` が使用可能 source を指定する
- `measurement_valid` と `invalid_reasons` を必ず持つ(§6.4)
- series は**グラフ/診断用**。promotion 判定は `stats` を使う

### 5.3 上限(operator ceiling、Agent/recipe は下げるのみ)

| 制限 | 既定 |
|---|---|
| `MaxEvidenceBytes` | 16 MiB |
| `MaxSeriesPointsPerSeries` | 4096 |
| `MaxTotalSeriesPoints` | 65536 |

超過は seal 失敗(`run` は failed、理由を記録)。downsample は**決定論的**(等間隔)に行い、`original_count` と `sample_method` を残す。

### 5.4 収集の分担

| source | 実体 | 用途 |
|---|---|---|
| trusted driver | operator が用意する固定ドライバ(Sandbox 内で実行) | decode/prefill の **primary** |
| harness | 外側 wall clock・フェーズ所要時間 | 記録・回帰監視 |
| external_gpu | `nvidia-smi`(MVP)/ DCGM(将来) | VRAM、utilization、clocks、temp、power、Xid | 
| runtime | llama.cpp の `/metrics` 等 | キャッシュヒット、投機受理、VRAM 内訳(**診断**。primary には使わない) |
| profiler | Nsight(gray zone のみ) | kernel 内訳(診断) |

driver は harness から port-forward 越しに動かさない(ネットワーク jitter が primary latency に混ざる)。**「runtime の外で測る」= candidate が変更できないコードで測る**、という意味に固定する。

**信頼境界(実装契約)**: 同一 Pod・同一 filesystem・同一 UID では candidate が driver binary や collector 出力を書き換えられるため、次を必須とする。

- driver/collector は **candidate が書き込めない場所**から実行する(読み取り専用マウント / 別コンテナ / root 所有の非書込パス + candidate を非 root で実行、のいずれか)
- **出力チャネルも candidate から保護する**(実行物だけでは足りない)。次のいずれかに固定する:
  - driver の stdout を sandboxd transport 経由で harness が直接 capture する(推奨。candidate が触れるファイルを経由しない)
  - または `raw-measurement.json` を **candidate が書き込めない専用ディレクトリ**(root 所有・非書込)に置く
- protocol の実行契約に **ディレクトリ/ファイルの ownership** まで含める(誰が書ける場所か)
- `MeasurementProtocolDigest` には **driver と collector の content digest**(ファイルツリーの digest)を含める。version 文字列だけでは不足
- harness は回収した `raw-measurement.json` を**自分で validate/normalize/canonical 化**して evidence を書く(Sandbox に最終 evidence を書かせない)

### 5.5 measurement validity gate

`measurement_valid` は **「測定自体が信頼できるか」だけ**を表し、**候補の性能や正しさを表さない**。次のいずれかを検出したら `false` とし、性能比較から除外する(`decide` は `inconclusive`):

- 同じ GPU 上の foreign process(他ジョブ)
- サーマル/パワーリミットによるスロットリング、期待しない clock 変化
- ホストの異常 CPU 負荷・バックグラウンドビルド
- collector の欠測(gap)、測定チャネルの故障
- 測定環境の故障(環境起因の Xid・device lost 等)

一方、**valid な evidence の上での候補の失敗は reject** であって invalid ではない:

| 事象 | 扱い |
|---|---|
| correctness gate 失敗(valid evidence) | `reject`(Session は non-retryable) |
| 候補起因の OOM / resource violation(valid evidence) | `reject`(non-retryable) |
| 候補起因の Xid(例: カーネルが候補の不正なカーネル起動で落ちた) | `reject`(non-retryable) |
| 環境起因の Xid / device lost | `measurement_valid=false` → `inconclusive` |
| infra 障害で evidence が取れない | `inconclusive`(Session が新規 Run で再測定) |

これが無いと「0.8 ms 改善」の実体がバックグラウンドビルドの有無になる。

---

## 6. MeasurementProtocol と PromotionPolicy

### 6.1 分離の理由

「どう測ったか」と「どう採否するか」を同じ digest にすると、**閾値変更だけで比較不能**になる。両方を operator 所有にし、**snapshot 本体と digest の両方**を保存する(operator config を書き換えても当時の測定条件を復元できるようにする。HookPlan と同じ考え方)。

```
MeasurementProtocol: schema/version, driver version, workload matrix, context depths,
  warmup, KV fill procedure, repetition count, execution order, collector config,
  collector interval, runtime reset policy, required metric sources

PromotionPolicy: primary objective(name, direction=min), minimum effect size,
  noise threshold, regression limits, VRAM headroom minimum, correctness predicates,
  retry/gray-zone rule, allowed metric sources
```

保存先: `Run` に `MeasurementProtocolID / MeasurementProtocolJSON / MeasurementProtocolDigest`(submit 時 snapshot)。`PromotionPolicyID / PromotionPolicyJSON / PromotionPolicyDigest` は **D で型と canonical snapshot を定義**し、**永続化(session への束縛)は E** で行う。

**選択入力は submit 時のみ**で、recipe からは指定できない:

```
SubmitOptions:
  kind: visual | measurement          # 省略時は visual(operator profile の既定に従う)
  measurement_protocol: <operator-owned-id>
```

規則:

| 入力 | 要求 |
|---|---|
| `kind: measurement` | `measurement_protocol` **必須**。target の allowlist に無ければ 400 |
| `kind: visual` | `measurement_protocol` は任意(付ければ visual + evidence) |
| profile 経由 | profile が `kind` と `protocol` を固定し、**caller は上書きできない**。省略時の既定も profile が決める |

server 側は operator の `measurement_protocols`(本体)と `targets.<id>.measurement_protocols`(allowlist)に照合してから snapshot を固定する。optimization では operator の `optimization_profiles` が protocol と policy を bind する:

```yaml
optimization_profiles:
  v100-llamacpp:
    protocol: qwen-flash-longctx-v1
    promotion_policy: latency-v1
    max_rounds: 20
    max_runs: 160
    max_gpu_seconds: 21600
    max_window_duration: 2h
```

### 6.2 判定は harness(Agent に採否権を与えない)

`llmbench optimize decide` は **2層**にする。store / evidence の I/O は `LoadDecisionInput`、判定は `Decide(input) Verdict` の純関数で、CLI は load → decide の wrapper にすぎない。

```go
func LoadDecisionInput(ctx, store, baselineRunIDs, candidateRunIDs []string) (DecisionInput, error)
func Decide(in DecisionInput) Verdict   // 純関数。VerdictDigest はここから決定論的に決まる
```

**「期待される欠落」と「破損」を分離する**:

| 状況 | 扱い |
|---|---|
| 失敗 run / infra 障害で evidence が無い(記録された `MetricsDigest` も空) | 正常。`DecisionInput` の `EvidencePresent=false` → `Decide` は `inconclusive` |
| `MetricsDigest` は記録済みなのに file が無い / digest 不一致 / malformed | **`LoadDecisionInput` がエラー(fail closed)**。判定不能と混同しない |

`DecisionInput` には comparability gate に必要な provenance と sealed metrics を**すべて**含める(store を内部で読む `Decide` は純関数ではない):

```
DecisionInput:
  baseline/candidate: [{RunID, Kind, ModelTreeDigest, RuntimeSpecDigest, RuntimeBuildDigest,
                        EnvironmentDigest, WorkloadDigest, MeasurementProtocolDigest,
                        MeasurementProtocolSnapshot, MetricsDigest, MetricsJSON}]
  PromotionPolicyID / PromotionPolicySnapshot / PromotionPolicyDigest
  AlgorithmVersion
```
出力:

verdict は `accept | reject | inconclusive`。**`inconclusive` は「候補の良し悪しを判定できない」**ケース(measurement_valid=false、collector gap、foreign process、infra 障害による evidence 欠落)であり、reject とは区別する。

```json
{
  "verdict": "accept",
  "primary_metric": "decode_step_ms",
  "baseline": 18.42, "candidate": 17.71, "improvement": 0.71,
  "threshold": 0.45,
  "guards": {"correctness": "pass", "prefill_regression": "pass", "vram_headroom": "pass", "validity": "pass"},
  "evidence": {"baseline": ["<metrics_digest>"], "candidate": ["<metrics_digest>"]},
  "policy_digest": "...", "algorithm_version": 1
}
```

verdict も immutable(`VerdictDigest`)として session 台帳に残す。Agent は候補提案・実行要求のみを行い、**accepted runtime を書き換えるのは harness の verdict だけ**。

### 6.3 辞書順ゲート

```
1. correctness gate          : perplexity/logit tolerance 等(pass 必須)
2. resource/safety gate      : GPU あたり VRAM headroom >= 512 MiB、OOM/Xid なし
3. comparability gate        : §7 の must-equal + measurement_valid
4. primary objective         : decode_step_ms を最小化
5. regression guards         : prefill 回帰 <= X%、VRAM、必要なら cache
5.5 (machine) 判定不能        : measurement validity 欠落・collector gap・infra 障害 → `inconclusive`(reject とは別。Session が再測定を判断する)
6. human quality gate        : **machine ゲート 1〜5 の外側**にある最終 release/adopt ゲート(最終候補のみ visual A/B。内側ループでは行わない)
```

### 6.4 noise-aware 判定

- 反復は **pair 単位の balanced randomization**(pair1: A→B、pair2: B→A、pair3: A→B)。seed と実行順を evidence に記録する。`AAA BBB` は時間ドリフトに弱く、常に `AB` は order effect を持つ
- MVP は **3 pairs = 6 runs**。adaptive(N=3→5→7)は後段
- 閾値は最初は operator 固定値:

```
required_improvement = max(absolute_floor, relative_floor * baseline_median)
```

  MAD は診断表示のみ(N=3 では弱い)。履歴が貯まったら同一 protocol / environment class の repeatability から自動校正する
- 閾値未満の差は「改善」と扱わない。gray zone は Nsight の kernel time 合計などで確認する

### 6.5 最小 metric 集合

必須: `decode_step_ms`(+`step_count`)、`prefill_ms` または `prefill_tok_s`、`wall_clock_ms`、correctness pass/fail、GPU ごとの `peak_vram_used` / `min_vram_headroom`、OOM/Xid/failure、foreign process 等の contamination 情報。
条件付き: speculative accepted tokens/step、draft acceptance、cache hit/miss、CUDA graph hit/miss。
診断: GPU utilization、memory bandwidth、kernel time、Nsight breakdown。

`EnvironmentDigest` には**安定した条件だけ**を入れる(GPU model/count、driver、CUDA/toolkit ABI、power limit、clock policy、CPU、RAM、target class)。温度・瞬間クロック・他プロセスは **evidence の validity signal** であり digest には混ぜない。

---

## 7. Comparability

`compare --kind model|runtime` が must-equal / may-differ を判定する。metric **値**は絶対に fingerprint に入れない(入れるのは測り方)。

| | must equal | may differ |
|---|---|---|
| `--kind runtime`(runtime 最適化) | model tree digest、workload(`WorkloadDigest`)、`MeasurementProtocolDigest`、`EnvironmentDigest` | `RuntimeSpecDigest`、`RuntimeBuildDigest` |
| `--kind model`(モデル比較) | `RuntimeBuildDigest`、workload(`WorkloadDigest`)、`MeasurementProtocolDigest`、**`EnvironmentDigest`** | model tree digest |

- 既存 `BenchmarkFingerprint` は visual A/B 用として据え置き(変更しない)
- **`WorkloadDigest`** を新設する(protocol が指定する workload matrix のうち、その run が実際に実行した workload の canonical digest)。protocol 全体の一致だけで足りる場合は追加要求しない
- `MeasurementProtocolDigest` の内訳(schema 版、driver の content digest、workload matrix、warmup、KV 充填手順、反復数、**実行順序の規則**、collector 設定と content digest)は operator 側に置く。**objective は含めない**(`PromotionPolicy` の所属)
- balanced randomization は「規則」が protocol、「実際の AB/BA 順と seed」が evidence と session 台帳に属する

---

## 8. OptimizationSession

```
OptimizationSession
  profile / baseline runtime / accepted runtime / current round
  runs(独立 Run の集合)/ verdict history / cumulative GPU time / status
```

- **1 attempt = 1 Run**。`Round` = 1 候補変更 + build + 1..N run + 採否。`Session` = Issue から最終 PR まで
- 台帳は機械可読(round 表の正本)とし、人間向け round 表はここから生成できる
- budget は operator 所有。Agent/Issue 側は**下げることだけ可能**:

| budget | 例 |
|---|---|
| `MaxRounds` / `MaxRuns` | 20 / 160 |
| `MaxGPUDuration` / `MaxWindowDuration` | 6h / 2h |
| `MaxBuildDuration` / `MaxInfraRetries` | 30m / 3 |

- **retry は同一 Run を再実行しない**(`ExecInvoking` からの復帰を再実行しない原則を維持)。retryable な infra failure は Session が**新しい Run を作る**。分類:

| retryable | not retryable |
|---|---|
| transient sandbox transport | correctness failure |
| collector unavailable | candidate 起因の OOM |
| target preparation failure | benchmark exit != 0 |
| | invalid evidence |

- server-side `preflight` を用意する(operator policy、target availability、protocol の存在、runtime/image の存在、model pin、collector の可用性)。GPU を取る前に落とせるものは全部落とす

---

## 9. Agent CLI 契約

既存コマンドに加えて、**Agent は原則 HTTP API のみ**を使う(ConfigMap を直接触らせない)。

| コマンド | 目的 |
|---|---|
| `submit --remote <url> --request-id <key> [--wait] --json` | leader への唯一の正規経路。**ローカル submit は local target 専用**と明示 |
| `status --remote <url>` / `--coordination-namespace <ns>` | クラスタの run を照会 |
| `list` / `wait` / `logs` / `metrics` / `metrics diff` | 監視・解析 |
| `compare --kind model\|runtime <a> <b> --json` | 比較可否と差分 |
| `preflight`(ローカル + server-side) | 事前検査 |
| `preview <run-id>` / `context --json` | Issue 用 URL / 環境サマリ |
| `optimize start\|round\|decide\|status --json` | session 運用 |

### 9.1 idempotency(`--request-id`)

- scope は **repository/controller store 全体**。TTL は無し(Run が存在する限り binding も存在する)
- `RunID = Truncate128(SHA256(repository_namespace + "\0" + request_id))` と**決定論的に導出**する。Run に `RequestID` と `RequestDigest` を保存するので、別途 idempotency store は不要
- 同 request-id + 同 RequestDigest → **既存 Run を返す**。同 request-id + 異なる digest → **409 Conflict**
- **RequestDigest は canonical な「submit 内容全体」**の digest とする。少なくとも recipe snapshot identity、input commit、target、**`Kind`**、measurement protocol ID/digest、workload selector、(あれば)window ID を漏れなく含める(`Kind` を独立させたため、visual → measurement の変更も 409 で検出できる必要がある)

### 9.2 exit code

| code | 意味 |
|---|---|
| 0 | 成功 |
| 2 | 入力不正(recipe・path・引数) |
| 3 | 競合(lease busy、digest 不一致、request-id 衝突) |
| 4 | 人間待ち(pause PR 未マージ等) |
| 10 | run 失敗(`reason` で `execution_timeout` 等を区別) |
| 11 | client の `--wait` timeout(**Run は cancel しない**。同 request-id で再 attach) |

`--json` を全コマンドで受け付ける。エラーは stderr に `{"error":{"code","message","hint"}}`。POST 成功(`RunID` 確定)と `--wait` は分離する。

---

## 10. Runtime supply chain

- `runtimes/<engine>/<variant>/{runtime.yaml,patches/,vendor/}`。`RuntimeSpecDigest = sha256(upstream commit + patch digests + build argv + outputs 宣言)` は submit 時に既知
- `RuntimeBuildDigest = sha256(ビルド成果物ツリー)` は実行後に確定し、`EnvironmentDigest`(toolkit)とともに provenance に記録
- build cache は PVC 上で `build-key = sha256(RuntimeSpecDigest + dev image digest + toolkit 版)`。ヒット時はツリー digest を照合してビルドをスキップ。ミス時は構築して atomic rename で封入する
- **build は `ExecutionState=invoking` より前の冪等な preparation として write-ahead 管理する**(または Run 作成前の session-level build として完了させる)。`Executor` から `ErrPending` を返して同一 Run を再試行させる設計は**禁止**(v1.6 の「invoking から同一 Run を再実行しない」と衝突する)。`ready_timeout` 超過は preparation 段階の `ErrPending` として扱い、`invoking` に入る前なら安全に再試行できる
- 供給網: **`RuntimeSpecDigest` は fingerprint に入れない**(§7)。release は OCI image として行い、**identity は OCI digest**(tag は移動可能な alias)
- **CUDA build に GPU は不要**(`CMAKE_CUDA_ARCHITECTURES` + `GGML_NATIVE=OFF`)。ただし**ビルドとベンチを同時に走らせない**(測定のホスト干渉を避ける)
- ランナー役割: `llm-bench-verify`(untrusted PR・ephemeral)/ `llm-bench-image`(trusted・main のみの Linux builder)/ GPU ノードはベンチ専用
- workflow は main のみ、`contents: read` + `packages: write` + `attestations: write` + `id-token: write`、attestation/SBOM 付き、タグ `:<git-sha>` と `:<variant>`、deployment は digest pin
- 段階: まず「source build → `RuntimeBuildDigest` 記録 → main merge → GHCR 生成」。**benchmark した binary と公開 image が bit-for-bit 同一**である保証は、先に candidate OCI image を作り**その digest をベンチして同一 digest を promote** する段階で得る(それまでは「同一 source/patch/image から再ビルド」の保証に留まる)

---

## 11. 公開と adopt

- `review request` の marker は **v2** を追加する(schema version で v1/v2 両方を読む):

```json
{"schema_version": 2,
 "baseline":  {"run_id": "...", "artifact_digest": "...", "metrics_digest": "...", "measurement_protocol_digest": "..."},
 "candidate": {"run_id": "...", "artifact_digest": "...", "metrics_digest": "...", "measurement_protocol_digest": "..."}}
```

  v1 marker は「visual review として有効、metrics をレビューしたとは主張しない」として許容する。metrics 付き run は v2 を生成する
- 人間が数値を見る場所は **① Issue の差分表**(review request が主要 metric の delta をコメントに含める)、**② 公開サイトの wrapper ページ**(採用 evidence から site build 時に決定的に SVG を生成)。preview は `index.html` のみのまま
- adopt は visual artifact を要求し、evidence を同伴する(manifest **v2**):

```json
{"schema_version": 2, "artifact_digest": "...",
 "evidence": {"metrics_digest": "...", "measurement_protocol_digest": "..."}}
```

  `experiments/<model>/<exp>/evidence/metrics.json` を git へ materialize する。`ArtifactDigest` には evidence を含めない。manifest v1 も引き続き verify / site build できること。SVG は git に置かず site build で生成する

---

## 12. テスト方針

- `decide` は純関数として table-driven: 各ゲートの境界(閾値ちょうど、noise 閾値未満、headroom 不足、validity false、同点)、verdict digest の安定性
- evidence: schema 検証、上限超過、canonical 化の決定性、seal の durability(親 dir fsync 前に digest を保存しない)、`PullLimited` の回収上限
- Window: `ActiveRunID` の CAS 競合(2 worker)、`open` 以外の拒否、`closing` 中の新規 run 拒否、release エラー中に `closed` にならないこと、`max_window_duration`/`idle_timeout` での収束、recovery 表(§4.7 の全行)
- comparability: must-equal 違反での拒否、`--kind` ごとの差分
- idempotency: 同 request-id 再送で同一 Run、digest 不一致で 409
- fake client-server で `POST /v1/preflight` と `GET /v1/runs/{id}/metrics` の応答、認可(control API のみ)

---

## 13. 今やらないこと

- Run 内部の multi-attempt、Window 内での SandboxClaim 共有、Window 間での runtime プロセス再利用
- 同一 Run の自動再実行
- Issue 作成を run の自動トリガーにすること
- Agent 自身に promotion 権限を与えること、runtime 自己申告 metric を primary objective にすること
- 単一の weighted score、全 run での Nsight、raw Nsight trace の git 保存
- regression dashboard、汎用 collector plugin framework、llm-bench 内蔵の Prometheus 時系列 DB
- adaptive N(3→5→7)と noise 閾値の自動校正(履歴が貯まってから)
- SVG の具体デザイン、DCGM 採用有無、CUDA builder の実ホスト、GHCR tag の細部、Issue Form の見た目、Agent skill 本文

---

## 14. 実装フェーズ

| # | 内容 | 完了条件 |
|---|---|---|
| A | Measurement identity | **`ExecutionOutputs` 戻り型**、`LeaseState=not_applicable`、`RunKind` と protocol の独立、`MetricsDigest` / `MeasurementProtocol*`(driver content digest 込み)/ `WorkloadDigest` / `RuntimeSpecDigest` / `RuntimeBuildDigest` / `EnvironmentDigest` を Run provenance に追加。既存 `BenchmarkFingerprint` を壊さない。objective/threshold は入れない |
| B | Sealed evidence | `evidence/metrics.json`(schema、source/trust、validity、上限、atomic seal、durability)、`GET /v1/runs/{id}/metrics`、`llmbench metrics --json`。collector は harness timing + nvidia-smi + runtime `/metrics`(任意) |
| C | Compare + remote Agent CLI | `submit --remote --request-id --wait --json`、`status --remote`、`wait`/`list`/`logs`、`preflight`(local + server-side)、`compare --kind model\|runtime`、exit code 契約 |
| D | Promotion policy | `PromotionPolicy` の型 + canonical snapshot/digest、`optimize decide`(純関数)、`Verdict`(`accept\|reject\|inconclusive`)+ `VerdictDigest`。**永続化(台帳)は E** |
| W | MeasurementWindow | `OwnerRef` 一般化、Window 状態機械、`ActiveRunID` CAS、timeout/idle close、recovery、engine の分岐 |
| E | OptimizationSession | session 台帳(**PromotionPolicy snapshot と verdict history の永続化**)、budget、Issue intent(`kind: benchmark\|optimize`)、round 記録、failure 分類と再試行(同一 Run を再実行しない) |
| F | Runtime spec | `runtimes/<engine>/<variant>`、`runtime verify`、spec/build digest、build cache |
| G | GHCR release | main-only builder、digest pin、attestation/SBOM |
| H | Public metrics | adopted evidence 契約(manifest v2)、site SVG、review 差分表 |

A〜D は単一 Run でも完成する。Window(W)は D の後・E の前。
