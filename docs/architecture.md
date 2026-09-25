# llm-bench コントローラ実装設計書(v1.5)

> この文書は `docs/design.md`(ドメイン要件)・`docs/usage.md`(機能仕様)・`AGENTS.md`(規約)を実装に落とすための設計 blueprint である。
> ChatGPT 等の外部レビューに単体で渡せるよう、背景要件から実装方針までを自己完結して記述する。
> ステータス: v1.5 — 外部レビュー 5 周目のブロッカー 2 件(依存循環の解消、Release 時のエラー意味論の一本化)+ 非ブロッカー 3 件を反映(§11)。確定後にマイルストーン 1 から実装開始。

---

## 1. 背景と要件

### 1.1 このシステムは何か

LLM に HTML で 3D シーンを作らせる visual benchmark の実行を管理するコントローラ(CLI + HTTP サーバ)。
生成物の善し悪しは**人間が A/B 判定**し、その記録は GitHub Issue が正とする。
エージェント(Claude 等)は実験の提案と分析を書くが、**実行そのものにエージェントは不要**。

リポジトリ契約:

```
benchmarks/visual/prompt.md        # 共有ベンチマーク課題
models/models.yaml                 # モデルカタログ(models/<id>/ 本体は Git 管理外)
experiments/<model-id>/<exp-id>/   # config.yaml(実験recipe) + README.md(仮説・考察) + output/
charts/llmbench/                   # 実験的 Helm Chart(単一replica)
cmd/llmbench/                      # Go コントローラ(本設計の実装対象)
```

### 1.2 実行ワークフロー(要件の核心)

1. 明示的なリクエスト(CLI/HTTP)だけが run を開始する。Issue や PR の作成・編集は run を消費しない
2. 推論ワークロードの停止は **GitOps**: **ベンチリポジトリとは別のマニフェストリポジトリ**(オペレータ設定の `owner`/`repository`/`base_branch`)の特定 YAML スカラーを `active_value` → `paused_value` に変える PR をコントローラが作り、**人間がマージ**する。Argo CD 同期と推論 Pod 停止を確認してから GPU を使う
3. GPU 実行は Agent Sandbox(k8s CRD + Pod port-forward の sandboxd)内で行う。Claim 名は run ID から決定論的に導出される
4. **取得順の設計不変条件: 推論 Pod の停止確認(GitOps pause 完了)→ GPU Sandbox claim 取得。解放は厳密に逆順: Sandbox プロセス停止+claim 解放 → GitOps restore → …。この順序は operator 設定に対する検証ルールとして機械的に強制する**(§4.2, §4.6)
5. 実行後は成果物を Sandbox 外へ保存し、Claim を解放し、**復帰 PR** を作る。**復元は実行が失敗しても必ず行う**。target claim の解放が完了するまで run を terminal にしない
6. 公開サイトへ HTML を冪等公開し、Issue に baseline/candidate を投稿、人間が A/B/tie/invalid で投票。Discord は Issue へのリンク通知のみ
7. 1 target で同時実行は不可(**submit は可能。後続 run は claim 解放待ちとして待機**)。**異なる target は並行実行可**

### 1.3 コントローラ境界(権限分離)

- 実験 YAML(`experiments/.../config.yaml`)が指定できるのは model / benchmark / **allowlist 済 target ID** / runtime 設定(start・invoke を含む recipe)/ context のみ。**実験 YAML に特権設定(GitOps パス・GPU・認証情報・公開先・タイムアウト上限)は書けない**
- target の実体(GitOps 対象、Sandbox pool、hook コマンド、公開サイト、レビュー設定、**実行時間の上限値**)は**オペレータ設定**(別 YAML、CLI フラグで指定)のみに存在する
- 認証情報は環境変数または k8s Secret のみ。Sandbox Pod と実験コマンドのプロセスに認証情報を渡さない
- SSH 実行経路は存在しない
- **local target は信頼された開発用途専用であり、Sandbox と同等のセキュリティ境界を提供しない**(os/exec の子プロセスはコントローラと同一の filesystem/network 権限を持つ)。HTTP 経由での local target 実行は**デフォルト禁止**(§4.11)

### 1.4 機能要件一覧(実装スコープ)

| # | 要件 | 出典 |
|---|------|------|
| F1 | 実験 recipe のパース・バリデーション | usage.md |
| F2 | オペレータ設定(target allowlist / hooks / gitops / sandbox / site / review / **limits**)のパース・バリデーション。**hook 順序不変条件の検証を含む** | usage.md |
| F3 | run 状態機械 + 順序付き acquire/release hook + 冪等リカバリ(**claim・実行状態を含む全外部効果の write-ahead**) | design.md |
| F4 | コマンドフック: 引数配列を shell なしで実行、**exit 75 = acquisition pending** | usage.md |
| F5 | ローカル実行ランナー(argv 実行、`LLMBENCH_*` 環境変数は allowlist 型で注入、認証情報は継承しない) | usage.md |
| F6 | Sandbox 実行ランナー: コミット検証 → archive アップロード → **長寿命プロセスとして `runtime.start`**(ready 確認)→ `invoke` → 成果物回収。**recipe は submit 時 snapshot を使用** | design.md |
| F7 | 来歴記録: BenchmarkFingerprint・成果物 hash(Sandbox 外保存の瞬間に計算)・モデル tree digest(symlink/欠落拒否、オペレータ pin と照合) | design.md |
| F8 | 1 target 1 実行の atomic claim。**取得・解放とも write-ahead で、解放は owner 条件付き削除。解放完了前に terminal にしない** | design.md |
| F9 | 永続化: ローカル file store(単プロセス)/ k8s ConfigMap store(**不透明な Version 文字列による CAS**)+ Lease リーダー選出 | usage.md |
| F10 | HTTP API: run 投稿・状態参照・bearer 認証(**token 未設定時は loopback bind のみ**) | usage.md |
| F11 | CLI: validate / submit / status / serve / sandbox / review | usage.md |
| F12 | GitOps hook: 状態と帰属を考慮した冪等な pause/restore PR + Argo CD `Synced` 確認 + Deployment/Pod 状態確認(§4.6) | usage.md |
| F13 | Pages 公開: 冪等公開。**GPU 解放後に行い、成功実行のみ公開する。公開が成功するまで finalizing に留まりリトライ(公開失敗で failed にしない)** | 本設計で新設 |
| F14 | Issue A/B レビュー: marker 付きコメントの機械解釈(bot_login 制限)、投票履歴の正は Issue。比較条件は BenchmarkFingerprint、**同モデル比較時のみモデル digest 一致を追加要求** | usage.md |
| F15 | 再起動リカバリ + 並列 dispatch: run 毎 worker、異なる target の並行実行、30 秒周期の dispatch tick | usage.md |
| F16 | **実験 recipe・prompt の submit 時 snapshot**: run の再開は常に snapshot を使い、生きたリポジトリファイルに依存しない | 本設計 |
| F17 | **operator 側の強制タイムアウト上限**: 実験者が引き上げられない ready/実行時間の上限を target 設定に持たせ、recipe 側 timeout はその範囲内でのみ許可 | 本設計 |

### 1.5 非機能要件

- `go test -race ./...` と `go vet ./...` が常時緑。ポリシーロジックは外部 SDK なしでテスト可能
- 外部効果はすべて冪等。プロセスが任意の時点で死んでも再起動後に収束する(**外部呼び出し前に意図と進行状態を永続化する write-ahead 原則** — claim・hook・実行・プロセスのすべてに適用)。**結果の再現が不可能な処理(ベンチ実行本体)は、write-ahead からの復帰時に自動再実行せず失敗扱いにする**
- 実験 YAML を変えずにオペレータ設定だけで実行先を差し替え可能
- 依存は go.mod 固定分のみ。新規依存の追加は避ける

### 1.6 スコープ外

- Agent 最適化ループ(design.md が「未接続」と明記。将来 `runner` と同型の executor として接続する想定)
- モデルダウンロード(models.yaml の解釈はコントローラ外)
- invoke プロセスへの再アタッチ+completion receipt による実行再開(v1 は「interrupted = failure」を採用。将来拡張として §10 に記載)
- リアルクラスタでの e2e 検証、マルチレプリカ fence、Helm Chart の本格整備

---

## 2. 全体アーキテクチャ(Go 慣習版)

### 2.1 採用するスタイルと理由

**レイヤー名のディレクトリ(domain/usecase/repository/handler)は作らない。** 機能軸でパッケージを切り、interface は消費者側に小さく定義する。理由:

- 本リポジトリの AGENTS.md が既に「Cobra コマンドは `cmd/llmbench`」「HTTP は薄い `internal/httpapi`」「外部効果は注入可能に」「**小さい interface は使用点で定義**」と規定しており、Go 慣習版がそのまま整合する
- interface を port 専用パッケージに集約すると、実装との対応が間接化され、Go の暗黙的 interface 満足の利点(実装側は抽象を知らない)が薄れる

### 2.2 設計原則

1. **依存は一方向・循環禁止**: `cmd → {httpapi, 機能パッケージ}`、`httpapi → run(ポリシー)`、機能パッケージ同士は縦方向のみ
2. **interface は消費者側で宣言**: `run` が `Hook`/`HookSource`/`Executor`/`Finalizer`/`RunStore`/`LeaseStore` を、`review` が `Issues`/`Notifier` を、`runner` が `SandboxClient`/`Publisher`/`Git` を宣言する。実装パッケージはそれらを**暗黙に満たす**(抽象を import しない)
3. **SDK を import してよいパッケージを制限**: `sandbox` / `gitops` / `kube` / `issues` / `pages` / `discord` / `httpapi` / `cmd` のみ。`run` / `runner` / `review` / `experiment` / `operator` / `provenance` / `filestore` は SDK なし
4. **write-ahead + 冪等**: すべての外部効果(TargetLease、hook、実行、長寿命プロセス、PR、公開)は、呼び出し**前に**意図と進行状態を永続化し、決定論的名前で再実行可能にする。ただし**ベンチ実行本体は再実行不能なため、割り込みは失敗として扱う**
5. **グローバル状態なし**: ロガーは注入(logr)、時刻は `Clock` を注入、ID 生成は注入可能に
6. **合成は cmd のみ**: `cmd/llmbench`(`wire.go`)が「どの実装をどのポリシーに渡すか」を一箇所で決める

### 2.3 ディレクトリ構成

```
cmd/llmbench/
  main.go              # エントリポイントのみ
  root.go              # 共通フラグ(--root 等)とロガー初期化
  validate.go          # llmbench validate <experiment.yaml>
  submit.go            # llmbench submit <experiment.yaml> [--commit <sha>]  (完了まで同期)
  status.go            # llmbench status <run-id>
  serve.go             # llmbench serve --config ... [--state --output --retry-interval --coordination-*]
  sandbox.go           # llmbench sandbox acquire|run|pull|release(手動操作)
  review.go            # llmbench review request|vote|status
  wire.go              # ★配線: 実装の構築と注入(HookSource 含む)を集約

internal/
  experiment/          # 実験 recipe。型・パース・バリデーション・canonical serialization(純粋)
  operator/            # オペレータ設定。型・パース・バリデーション(順序不変条件・limits 検証を含む)
  run/                 # ★中核: Run 型・状態遷移・Engine(Dispatcher+worker)。
                       #   Hook/HookSource/Executor/Finalizer/RunStore/LeaseStore をここで定義
  runner/              # ベンチ実行の具体化。local.go / sandbox.go / finalize.go
                       #   SandboxClient/Publisher/Git interface をここで定義
  provenance/          # 来歴: sha256・BenchmarkFingerprint・モデル tree digest・
                       #   model-identity.json・git スナップショット検証(git バイナリを exec)
  sandbox/             # agent-sandbox SDK の薄いクライアント(SDK import 可)
  gitops/              # GitOps pause/restore hook(go-github + kube を使用)
  kube/                # k8s 全般: ConfigMap store・TargetLease store・Lease・Argo CD/Deployment/Pod 確認
  issues/              # GitHub Issue コメント操作(review.Issues を実装)
  review/              # A/B レビューのポリシー(marker 検証・投票・記録)
  pages/               # 静的サイト公開(runner.Publisher を実装、_headers 生成を含む)
  discord/             # webhook 通知(review.Notifier を実装)
  hook/                # コマンドフック(operator計画→run.Hook、exit 75→ErrPending)
  filestore/           # ローカル file 実装(RunStore/ReviewStore/LeaseStore)
  httpapi/             # chi HTTP サーバ(薄い)。認証・デコード → run の Service へ委譲
```

### 2.4 依存方向

```
cmd/llmbench ──▶ 全パッケージ(配線)

httpapi ──▶ run
runner ──▶ run, provenance, experiment, operator
review ──▶ run(型参照のみ)
run ──▶ experiment, operator(型のみ), logr
provenance ──▶ (なし。標準ライブラリ + git バイナリ)

sandbox ──▶ (agent-sandbox SDK のみ)
gitops ──▶ kube, (go-github)
kube ──▶ (client-go)
issues ──▶ (go-github)
pages ──▶ (go-github)
discord ──▶ (net/http)
filestore ──▶ (標準ライブラリ)
```

- **実装 → ポリシーの import は存在しない**(暗黙的型満足のため不要)
- `gitops → kube` は同一インテグレーション層内の参照として許容
- `runner` は SDK を import しない。`SandboxClient` / `Git` / `Publisher` の interface を満たす実装を cmd が注入する

### 2.5 実装状況

この設計に対する実装は、`main` にマージ済みの設計書に続いて、以下の単位で積み上げている:

| 単位 | 内容 |
|------|------|
| run state machine | `experiment` / `operator` / `run`(write-ahead 状態機械+Engine)/ `runner`(local)/ `hook` / `filestore` / `provenance`(fingerprint)、CLI `validate`/`submit`/`status` |
| serve + HTTP API | `httpapi`(認証・公開URL)/ serve の dispatcher・graceful shutdown・loopback 制約・local target の HTTP 既定拒否 |
| Sandbox runner | `sandbox`(決定論的 claim・argv境界quote・detachプロセス)/ `runner.Sandbox`(commit照合・archive転送・readiness・モデル digest pin) |
| GitOps hook | `gitops`(状態+帰属の決定テーブル・rollback・drift拒否)/ `kube`(Argo CD・Deployment確認) |
| 公開とレビュー | `pages`(単一ツリーコミットで冪等公開)/ `runner.PublishFinalizer` / `issues` / `discord` / `review`(fingerprint検証・marker投票) |
| Kubernetes 協調 | `kube`(ConfigMap store=CAS、TargetLease、Lease リーダー選出と喪失時の worker cancel) |

未検証・未実装: 実クラスタでの e2e(Argo CD / Deployment / SandboxClaim の遷移)、マルチレプリカの外部効果 fence、Agent 最適化ループの接続、Git LFS の materialize。

### 2.6 採用しないもの

- DI コンテナ、wire コード生成、グローバルロガー
- Argo CD 型付きクライアント(unstructured + 単一 source 限定)
- go-git(git バイナリ exec の方が `git archive` 等の完全性が高く、自前実装が減る)
- chromedp / PNG preview(→ §7 の変更点)
- 汎用プラグイン機構・リフレクション

---

## 3. 中核ドメイン: run パッケージ

### 3.1 状態機械

**Phase(run 全体)、HookState(hook 毎)、LeaseState(TargetLease)、ExecutionState(実行)を分けて持ち、run を terminal にするのは最後の最後。**

```
Phase:  pending ──▶ acquiring ──▶ running ──▶ releasing ──▶ finalizing ──▶ succeeded
              claim取得+          │ ExecutionState       │                      or failed
              hook順次acquire     │  write-ahead         │ release失敗
                          │       │                      │ → releasing に留まり
                          │ ErrPending /                 │   worker が interval リトライ
                          │ claim busy は同 phase         │
                          │ で interval リトライ          │
                          ▼                              ▼
                 (クラッシュ時は保存済み状態から冪等に再開)
```

- `Phase = pending | acquiring | running | releasing | finalizing | succeeded | failed`
- `ExecutionResult = none | success | failure`
- `ExecutionState = not_started | invoking | completed` — **実行本体の write-ahead**。`invoking` を保存してから Execute を呼ぶ。復帰時に `invoking` だった場合は**自動再実行しない**(実行の再現性がないため `ExecutionResult=failure`(interrupted)として releasing へ進む。§10.1 の将来拡張を除く)
- `HookState = not_started | acquiring | acquired | releasing | released` (+ `WaitReason`, `Error`)
- `LeaseState = acquiring | acquired | releasing | released` — target 排他(TargetLease)の取得・解放**両側**が write-ahead
- 旧 `awaiting_acquire` は「`acquiring` + 該当 hook が `acquiring` + WaitReason」、旧 `needs_restore` は「`releasing` + 該当 hook の Error」として**派生ラベル**として status 表示にのみ現れる

**write-ahead 原則(全外部効果に適用)**: 外部呼び出しの**前に**「これから行う」状態を永続化し、成功後に完了状態を永続化する。クラッシュが呼び出しと保存の間で起きても、再開時は「進行中」状態のステップを冪等に再試行する。**例外がベンチ実行本体(Execute)で、こちらは冪等でないため `invoking` からの復帰は再実行ではなく失敗とする。**

**rollback 経路は存在しない**: Acquire 失敗は専用ルートを作らず、`ExecutionResult=failure` を保存して**通常の `releasing` へ合流させる**。releasing の共通ロジックは `HookPhase` が `acquiring` / `acquired` / `releasing` の hook だけを逆順解放し、`not_started` は絶対に解放しない。例: PR 作成は成功したがレスポンス受信前にエラー → `Acquire()` は error を返すが外部効果は発生済み。対象に `acquiring` を含めることでこれを解放する。Release は冪等で、実効果が存在しなくても安全に呼べることを契約とする。途中で ErrPending(GitOps 復帰待ち、一時的な GitHub/k8s 障害)が起きても `releasing` に留まり再試行されるため、「復元は必ず完了する」要件が単一の経路で満たされる。

**ターミナル遷移の不変条件**:

- release は厳密逆順。hook i を解放するには i+1..n が `released` であること
- **TargetLease の解放は releasing フェーズの最後のステップ**(`LeaseState=releasing` を保存 → `ReleaseTargetLease`(NotFound=成功)→ `released` を保存)。terminal 書き込みは lease 解放保存の後でのみ行う
- releasing が完了する前に terminal にしない
- **releasing 完了後の分岐は ExecutionResult で決定**: `failure → failed`(公開処理は行わない。index.html が存在しないため)。`success → finalizing`
- `finalizing` は公開が成功するまで terminal に進まない(公開失敗は `PublishError` を記録して interval リトライ)。GPU は解放済みのため停留の実害はない
- 1 target の同時実行は LeaseStore が排除する。submit 自体は常に可能で、後続 run は `acquiring` で待機する

### 3.2 主要な型と interface(run パッケージで定義)

```go
type Phase string // pending | acquiring | running | releasing | finalizing | succeeded | failed
type ExecutionResult string // none | success | failure
type ExecutionState string // not_started | invoking | completed
type HookPhase string // not_started | acquiring | acquired | releasing | released
type LeaseState string // acquiring | acquired | releasing | released

// 用語: コントローラ側の target 排他を **TargetLease**、Agent Sandbox 側の GPU claim を
// **SandboxClaim** と呼び分ける。以下の LeaseStore は TargetLease のみを扱う

type HookState struct {
    Name       string
    Phase      HookPhase
    WaitReason string
    Error      string
}

// HookPlan の要素型 PlannedHook / GitOpsPlan / SandboxPlan は **operator パッケージ**で定義する
// (operator.BuildHookPlan が返す型のため。run → operator の一方向依存のみで済み、循環を回避。§4.2)。
// submit 時に snapshot される hook 計画(型付き・バージョン付き・認証情報を含まない)。
// digest は canonical JSON の sha256。HooksFor は毎回検証してから構築する(検証失敗は永続エラー)
// 実装上の Run のフィールド型: HookPlan []operator.PlannedHook

type Artifacts struct {
    Dir               string // 決定論的: <output>/runs/<runID>/ 。k8s モードでは必ず永続ボリューム上(§4.9 注記)
    IndexSHA256       string // Sandbox 外へ保存した瞬間に計算
    LogSHA256         string
    ModelTreeDigest   string
    ModelIdentityPath string
}

type Run struct {
    ID              string          // 32 hex
    Target          string
    Experiment      string          // リポジトリ相対パス(記録用)
    InputCommit     string          // Sandbox 実行では必須(40 hex)
    Fingerprint     string          // BenchmarkFingerprint(§4.8)
    // ── submit 時の recipe snapshot(F16)。再開は常にこれを使う ──
    RecipeSchemaVersion int         // snapshot 形式のバージョン
    RecipeJSON      string          // 検証済み experiment.Config の canonical JSON
    PromptSHA256    string
    // ── 進行状態 ──
    Phase           Phase
    ExecutionResult ExecutionResult
    ExecutionState  ExecutionState
    LeaseState      LeaseState
    Hooks           []HookState
    HookPlan        []operator.PlannedHook
    HookPlanDigest  string
    WaitReason      string
    PublishError    string          // finalizing の最終エラー記録
    Artifacts       Artifacts
    PublicURL       string
    StoreVersion    string          // 不透明な CAS トークン。ConfigMap: resourceVersion、filestore: "N"
    CreatedAt, UpdatedAt time.Time
}

type Hook interface { // HookSource が run 毎に構築。run ID 等は構築時に閉込める
    Name() string
    Acquire(ctx context.Context) error // ErrPending = 未準備。worker が interval 再試行
    Release(ctx context.Context) error // 冪等。実効果が存在しなくても安全に呼べること
}
type HookSource interface { // 永続化 Run(HookPlan snapshot)から hook 列を決定論的に再構成
    // 構築前に HookPlanDigest を再計算・検証する(不一致は永続エラー)
    HooksFor(r Run) ([]Hook, error)
}
var (
    ErrPending        = errors.New("run: not ready yet")    // exec は exit 75 をこれへ変換
    ErrLeaseBusy      = errors.New("run: target lease held") // 先行 run が保持中。待機対象
    ErrVersionConflict = errors.New("run: store version conflict")
)

type Executor interface {
    // 呼び出し前に worker が ExecutionState=invoking を保存すること(契約)。
    // 戻り時点で成果物は Artifacts.Dir へ確定保存+ハッシュ済みであること
    Execute(ctx context.Context, r Run) (Artifacts, error)
}
type FinalizeResult struct{ PublicURL string }
type Finalizer interface { // GPU 解放後、ExecutionResult=success の run に対してのみ実行
    Finalize(ctx context.Context, r Run, a Artifacts) (FinalizeResult, error)
}

// TargetLease の store。実装: filestore / kube
// 用語注意: SandboxClaim(Sandbox 側 GPU claim)は扱わない。そちらは sandbox.Hook が管理する
type RunStore interface { // file / ConfigMap 双方が実装
    // StoreVersion による CAS: 保存時の一致検証→不一致は ErrVersionConflict→成功時に新 Version を r へ設定
    SaveRun(ctx context.Context, r *Run) error
    LoadRun(ctx context.Context, id string) (Run, error)
    ListUnfinished(ctx context.Context) ([]Run, error) // non-terminal(phase != succeeded/failed)
}
// TargetLease の store。実装: filestore / kube
// 用語注意: SandboxClaim(Sandbox 側 GPU claim)は扱わない。そちらは sandbox.Hook が管理する
type LeaseStore interface {
    // 冪等: 同一 runID が owner なら成功。他 run が保持中は ErrLeaseBusy。atomic create
    AcquireTargetLease(ctx context.Context, target, runID string) error
    // 条件付き削除で完全に冪等:
    //   owner == runID → 削除して成功(k8s: UID/resourceVersion precondition 付き DELETE、
    //                     filestore: lock 下の owner 比較+削除)
    //   存在しない    → 成功(NotFound = success。releasing 保存後のクラッシュで
    //                     解放済みのときの再呼び出しを吸収する)
    //   owner != runID → 削除せず ErrLeaseBusy(古い run のリトライが次の run の
    //                     TargetLease を消さない)
    ReleaseTargetLease(ctx context.Context, target, runID string) error
}

type Clock func() time.Time

type Engine struct { /* RunStore, LeaseStore, HookSource, Executor, Finalizer, Logger, Clock, RetryInterval */ }
func (e *Engine) Submit(ctx context.Context, r Run) error
// Submit の snapshot 保存順(厳密):
//   1. <output>/runs/<runID>/input/ を temp dir に書く
//   2. fsync + atomic rename で snapshot 確定
//   3. Run(pending + LeaseState=acquiring + HookPlan snapshot)を保存
// この順により「Run だけ存在して snapshot がない」状態を作らない。
// 逆方向のクラッシュで残る孤立 input dir は起動時に GC 可能(既知の無害な残留)
// TargetLease 取得は worker が write-ahead で行う(submit 時に取ると「取得成功→保存前クラッシュ」で幽霊 lease が残るため)

func (e *Engine) Run(ctx context.Context) error       // Dispatcher ループ(serve 用)
func (e *Engine) Drain(ctx context.Context, runID string) error // submit 同期実行用: 指定 run を完了まで駆動
```

### 3.3 worker の駆動アルゴリズム(1 run の worker)

```
loop:
  r := store.LoadRun(id)
  switch r.Phase:
  case pending, acquiring:
    if r.LeaseState == acquiring:          # write-ahead 済(submit 時)。
      err := leases.AcquireTargetLease(r.Target, r.ID)
      - ErrLeaseBusy → save(WaitReason="target busy")、interval 後再試行
      - その他の error → save(ExecutionResult=failure, Phase=releasing)
        # 取得が実は成功していた曖昧ケースも、冪等な ReleaseTargetLease が回収する
      - 成功 → save(LeaseState=acquired)
    hooks[未完了の最初の 1 個]:
      save(Hooks[i].Phase=acquiring)       # write-ahead
      err := hook[i].Acquire(ctx)
      - ErrPending → save(WaitReason)、interval 再試行
      - 失敗 → save(ExecutionResult=failure, Phase=releasing)
               # 専用 rollback 経路は存在しない。通常の releasing に合流し、
               # 共通ロジックが acquiring/acquired hooks を逆順解放する
      - 成功 → save(acquired)。全 hook acquired → running
  case running:
    if r.ExecutionState == invoking:       # 実行中割り込みからの復帰(再 Execute しない)
      save(ExecutionResult=failure, WaitReason="execution interrupted", Phase=releasing)
      continue
    if r.ExecutionState == completed:      # 状態破損(completed なのに running)
      save(ExecutionResult=failure, WaitReason="inconsistent state", Phase=releasing)
      continue
    # 正常開始は not_started からのみ
    save(ExecutionState=invoking)          # write-ahead
    ctx timeout = operator Limits.MaxExecutionDuration(F17)
    a, err := executor.Execute(ctx, r)     # 成果物は Dir へ確定保存+ハッシュ済みで返る
    save(ExecutionState=completed, ExecutionResult=success|failure, Artifacts=a) → releasing
  case releasing:
    # 解放対象 = HookPhase が acquiring / acquired / releasing の hook のみ(not_started は触らない)
    # 厳密逆順で 1 個ずつ:
    save(Hooks[i].Phase=releasing) → err := hook[i].Release()
    - 成功 → save(Hooks[i].Phase=released)
    - ErrPending → save(WaitReason)「待機」分類、interval 再試行
    - その他の error → save(Hooks[i].Error) して releasing に留まり interval 再試行
      # error 種別を問わず released にせず、TargetLease も解放せず terminal に進まない。
      # ErrPending は UI 表示上の「待機」分類に過ぎない(不変条件の構造的担保)
    hook 解放完了後:
    save(LeaseState=releasing) → err := leases.ReleaseTargetLease(...)
    - nil   → save(LeaseState=released)
    - error → LeaseState=releasing のまま interval 再試行(NotFound は成功扱いの契約)
    # lease released の保存後のみ分岐する:
    - ExecutionResult == failure → **failed**(finalizing を経由しない)
    - ExecutionResult == success  → finalizing
  case finalizing:
    res, err := finalizer.Finalize(ctx, r, r.Artifacts)  # Artifacts.Dir から読み公開
    - 失敗 → save(PublishError)、interval 再試行(terminal に進まない)
    - 成功 → save(PublicURL=res.PublicURL) → succeeded
  case succeeded, failed: return
```

- **1 ステップ 1 保存**。任意の時点で死んでも、保存済み Phase/HookState/LeaseState/ExecutionState から同一ステップを冪等に再開する
- **SaveRun が ErrVersionConflict を返した場合**: ローカルの Run を破棄して LoadRun し直し、保存済み状態から再判定する。**競合した SaveRun に対応する外部効果は実行してはならない**(write-ahead の保存が完了する前に外部効果を起こさない原則の帰結)
- `ListUnfinished` は non-terminal を返すため、finalizing で停留中の run も dispatch 対象に残る

### 3.4 Dispatcher(並列とリカバリ)

- `Engine.Run` は周期 tick(既定 30s、`--retry-interval`)ごとに `ListUnfinished` を走査し、**実行中でない run に worker goroutine を割り当てる**(run ID 単位の in-flight 排他)
- run 毎に worker が立つため**異なる target は並行して進む**。同一 target の後続 run は ErrLeaseBusy 待ちとして `acquiring` で待機し、先行 run の lease 解放後に自動前進する
- 起動時: `ListUnfinished` の全 run を dispatch 対象にする。**HookSource は Run.HookPlan(snapshot)から再構成し、構築前に HookPlanDigest を毎回検証する**。snapshot の Kind が実装として存在しない場合は永続エラーとして手動介入を要求する
- hook の長時間待機は禁止: PR マージ待ち等も一律 `ErrPending` を返し、worker が interval を置いて再試行する
- k8s モードの leader は、Lease を喪失した時点で自分が起動した全 worker の context を cancel する(milestone 6)

### 3.5 環境変数契約

実験コマンド・Sandbox 内コマンドに注入: `LLMBENCH_RUN_ID`, `LLMBENCH_PROMPT_PATH`(= snapshot の `input/prompt.md`), `LLMBENCH_OUTPUT_DIR`, `LLMBENCH_MODEL_ID`, `LLMBENCH_MODEL_PATH`(ローカル: `<root>/models/<id>`、Sandbox: `/models/<id>`), `LLMBENCH_CONTEXT_SIZE`。

**ローカル runner の子プロセス環境変数は allowlist 型**: `LLMBENCH_*` と `PATH`/`HOME`/`TMPDIR` のみを明示的に構成し、コントローラの認証情報(`LLMBENCH_GITHUB_TOKEN` 等)は継承しない。**ただし local プロセスはコントローラと同一の filesystem/network 権限を持つため、local target は隔離境界ではない**(信頼された開発用途専用。§1.3)。

---

## 4. パッケージ詳細

### 4.1 experiment(純粋)

```go
type Config struct {
    Model     string
    Benchmark string        // リポジトリ相対 prompt パス
    Target    string        // オペレータ allowlist の ID
    Runtime   struct {
        Engine, Variant string
        ContextSize     int
        Start *Start       // runtime 起動 recipe(任意)
    }
    Invoke struct{ Argv []string }
}
type Start struct {
    Argv                []string // 例: ビルド+サーバ起動(長寿命プロセスになる前提)
    ReadyTimeoutSeconds int      // 既定 300。operator の MaxReadyDuration を超えられない
    ReadyArgv           []string // 0 終了するまで反復実行する readiness 確認(既定: なし=待たない)
}
func Parse(r io.Reader) (Config, error)        // 未知フィールド拒否(yaml KnownFields)
func Validate(c Config, root string) error
func CanonicalJSON(c Config) ([]byte, error)   // snapshot 用の決定論的シリアライズ(ソートキー)
```

`runtime.start` は candidate の recipe の一部であり特権ではないため、**実験 YAML 所有**とする。ただしタイムアウト値は operator の上限内でのみ有効(F17)。

### 4.2 operator(純粋)

```go
type Config struct {
    Targets map[string]Target
    Site    *Site
    Review  *Review
    Defaults Limits      // target が省略した場合の既定上限
}

// BuildHookPlan は hook の並び順を決定する**唯一の箇所**:
//   command hooks(宣言順) → gitops(設定あれば) → sandbox-claim(設定あれば)
// Load/Validate はこの関数の結果に対して不変条件(gitops が sandbox-claim より先、等)を検証する。
// 検証対象を「設定ファイルの見た目」ではなくこの出力にすることで、順序の定義が一箇所に集まる
func BuildHookPlan(t Target, runID string) []PlannedHook // run ID 依存フィールド(ブランチ名・claim 名)を含むため runID を受ける

// hook 計画の要素型。run パッケージから参照される(operator → run 依存を作らないためここに置く。
// §2.4 の「run → operator(型のみ)」を維持し、循環を構造的に排除する)
type PlannedHook struct {
    PlanVersion int    // 1
    Kind   string      // command | gitops | sandbox-claim
    Name   string
    Command    []string      // kind=command
    GitOps     *GitOpsPlan   // kind=gitops(ファイルパス・YAML パス・値など、認証情報は含まない)
    Sandbox    *SandboxPlan  // kind=sandbox-claim(namespace・warm pool・claim 名など)
}
type GitOpsPlan struct {
    Owner, Repository, BaseBranch, FilePath string
    YAMLPath []string
    ActiveValue, PausedValue string
    PauseBranch, RestoreBranch string // 決定論的: llmbench/{pause,restore}-<runID>
    AppNamespace, AppName string
    WorkloadNamespace, Deployment string
    ActiveReplicas int
}
type SandboxPlan struct{ Namespace, WarmPool, SandboxClaimName string }
type Target struct {
    Hooks   []CommandHook // {Name, Acquire []string, Release []string} argv。両方必須
    GitOps  *GitOps
    Sandbox *Sandbox
    Limits  Limits        // MaxReadyDuration, MaxExecutionDuration(実験者が変更できない上限。F17)
    AllowHTTPLocal bool   // HTTP 経由の local 実行を明示的に許可(既定 false。local target 専用)
}
type Limits struct {
    MaxReadyDuration     time.Duration // 既定 10m。recipe の ReadyTimeoutSeconds はこれ以下のみ許可
    MaxExecutionDuration time.Duration // 既定 2h。Execute 全体の強制 context timeout
}
func Load(path string) (Config, error)
// Load/Validate の検証項目:
// - hook 順序不変条件(§4.6): BuildHookPlan の出力に対して、
//   gitops が sandbox-claim より先に来ることを強制(違反は設定エラー)
// - limits の正の値チェック
func (c Config) ValidateRecipe(e experiment.Config) error
// - target allowlist 含有、Start.ReadyTimeoutSeconds ≤ MaxReadyDuration など recipe 側上限検証
```

### 4.3 runner(実行の具体化、SDK なし)

- `local.go`: `os/exec` で argv を shell なし実行(環境変数は §3.5 の allowlist)。**prompt・recipe は `runs/<runID>/input/` の snapshot を使用し、生きたリポジトリファイルは読まない**。`input_commit` は検証しない
- `sandbox.go`:
  1. `git.VerifyCommit(commit, expected)` — **snapshot 化された recipe・prompt の期待 SHA256** をコミット内 blob と照合(`git show`。submit 後の recipe 差し替えを二重防御)。gitlink(submodule)検出で拒否
  2. `git.Archive(commit)` → `Put` で `/workspace/src` へ。`models/` は archive に含まれないため、モデルは Sandbox の PVC マウント(`/models/<id>`)から与える
  3. `Start(Start.Argv)` — **長寿命プロセスとして起動**。handle は決定論的名(`runtime-<runID>`)で冪等: 同一 handle が生存していればそれを返す
  4. `Exec(ReadyArgv)` を timeout まで反復実行(0 終了で ready)。timeout 上限 = `min(ReadyTimeoutSeconds, MaxReadyDuration)`
  5. 実行前後でモデル tree digest を Sandbox 内計算し比較。不一致は失敗。operator pin と不一致なら invoke 前に中止
  6. `Exec(Invoke.Argv)`(context timeout = MaxExecutionDuration)、ログ収集
  7. `output/index.html`・ログ・`model-identity.json` を Pull し **Sandbox 外の `Artifacts.Dir` へ保存。sha256 はこの保存の瞬間に計算**する。この保存が release の前提条件
  8. プロセス停止は release フェーズで行う(`Stop`): SandboxClaim の release hook が `Stop` → `ReleaseSandboxClaim` の順に実行
- `finalize.go`: `Artifacts.Dir` から読み、公開 → `FinalizeResult{PublicURL}`

```go
// プロセスハンドルは不透明な文字列 ID(決定論的: "runtime-<runID>")。
// 構造体を runner で定義すると sandbox 実装が runner を import する必要が生じ
// 「実装→ポリシー import なし」原則を崩すため string で受ける
type ProcessHandle = string

type SandboxClient interface { // 実装: internal/sandbox。argv を受け取る
    EnsureSandboxClaim(ctx, runID, warmPool string) error
    // 長寿命プロセス管理。冪等: 生存していれば既存 handle を返す
    Start(ctx, runID string, argv []string, env map[string]string, cwd string) (ProcessHandle, error)
    Stop(ctx, runID string, h ProcessHandle) error // 冪等
    Exec(ctx, runID string, argv []string, env map[string]string, cwd string) (stdout, stderr []byte, err error)
    Put(ctx, runID string, r io.Reader, dest string) error
    Pull(ctx, runID, path string) ([]byte, error)
    ReleaseSandboxClaim(ctx, runID string) error
}
// ExpectedFile は provenance パッケージで定義する(runner.Git が要求すると
// provenance 実装が runner を import する必要が生じ循環するため。runner → provenance のみ許可)
type ExpectedFile struct{ Path, SHA256 string } // snapshot 由来の期待値
type Git interface { // 実装: internal/provenance
    VerifyCommit(ctx, commit string, expected []ExpectedFile) error
    Archive(ctx, commit string) (io.Reader, error)
}
type Publisher interface { // 実装: internal/pages
    Publish(ctx context.Context, runID string, html []byte) (publicURL string, err error)
}
```

sandboxd がシェル文字列しか受け付けない場合、**argv→シェル文字列の quote は `sandbox` パッケージ内部の境界実装に限定**し、quote 専用のテーブルテストで担保する。interface 契約は argv のまま。長寿命プロセスも SDK に session/process primitive があればそれを、無ければ決定論的タグ付きのバックグラウンド起動(`Exec` 経由 + pgrep 相当の生存確認)で実装する(§10.1)。

SandboxClaim のライフサイクルは hook として登録(`sandbox.Hook`: Acquire=EnsureSandboxClaim、Release=Stop+ReleaseSandboxClaim)。

### 4.4 provenance(来歴、git バイナリを exec)

- sha256 ヘルパー、`model-identity.json` 形式定義
- `HashTree(dir)`: 正則ファイルのみ。symlink・欠落はエラー。レコード = `path \0 size \0 sha256` をソート連結して tree digest。Sandbox 内では同アルゴリズムのスクリプト(python3)を投入し、共通の digest 計算で比較

### 4.5 sandbox(agent-sandbox SDK)

SDK を包む薄いクライアント。`runner.SandboxClient`(Start/Stop/Exec/Put/Pull/Claim)と claim hook を提供。Claim 名・プロセス handle 名は run ID 決定論的 → 冪等。sandboxd は Pod port-forward で到達(Service 非公開)。**トランスポート断でもコマンドを自動再実行しない**(副作用があるため)。再実行は上位(worker)が write-ahead 状態に基づいて判断する。

### 4.6 gitops(状態+帰属を考慮した冪等 hook)

PR の宛先は**ベンチリポジトリとは別のマニフェストリポジトリ**(オペレータ設定 `targets.<id>.gitops` の `owner`/`repository`/`base_branch`)である。コントローラはベンチリポジトリを読み取りのみに限定し、書き込み権限はマニフェスト・公開サイト・Issue リポジトリに限定する。決定論的ブランチと PR はすべてマニフェストリポジトリ上に作成され、Argo CD Application はそのリポジトリを監視する。

単純な「現在値が期待値か」ではなく、**現在値が自分の run に帰属するものか**まで見て判定する:

```
Acquire(pause):
  manifest 値 == active_value:
      自分の pause PR(pause-<runID>)が open → 待機(ErrPending)
      自分の pause PR が存在しない → ブランチを作って PR 作成 → 待機(ErrPending)
  manifest 値 == paused_value:
      自分の pause PR が merged → 次の確認へ(Argo CD Synced、Pod 停止)
      自分の pause PR が open   → 待機(ErrPending)
      上記以外(他者が paused にした) → 永続エラー(推測で patch しない)
  その他の値 → 永続エラー
  drift ケース:
      自分の pause PR が merged なのに manifest が active_value
      → 永続エラー(他者が戻した。再 pause PR は作らない)

Release(restore): active/paused を反転させた同型の決定テーブル
  (paused → 自分の restore PR 作成/待機、active → 自分の restore PR が merged なら
   Argo 同期+Pod を active_replicas まで確認、他者が戻した場合は永続エラー)
  drift ケース:
      自分の restore PR が merged なのに manifest が paused_value
      → 永続エラー(対称に扱う)

rollback(pause PR が未 merge のとき):
  自分の open な pause PR を close。ブランチは削除
```

- PR は head ブランチ名(`llmbench/pause-<runID>` / `llmbench/restore-<runID>`)で検索して再利用 → リトライで PR 重複なし
- クラッシュしても決定テーブルが同一の動作に収束する(「マージ直後に死んだ」ケースも merged 判定で継続)
- Argo CD は unstructured で単一 source のみ。`Synced` at base ブランチ rev、Deployment/Pod の停止/`active_replicas` 到達を `kube` の関数で確認
- **順序不変条件**: gitops hook は sandbox-claim hook より先に acquire されることを operator.Load が検証(§4.2)。release は engine が厳密逆順で実行するため「GPU claim → 停止確認 → GPU 解放 → 復帰確認」が構造的に保証される

### 4.7 kube(client-go)

ConfigMap RunStore(**Run.StoreVersion として resourceVersion 文字列をそのまま不透明トークンとして扱う。数値化しない**)、atomic な TargetLease(ConfigMap create の取り合い。**ReleaseTargetLease は取得した owner の UID を precondition にした条件付き DELETE。NotFound は成功扱い**)、Lease リーダー選出(喪失時に自 worker の context を cancel)、Argo CD Application(unstructured)確認、Deployment/Pod 状態確認。`filestore` と同一の `run.RunStore` / `run.LeaseStore` を実装。

### 4.8 review / issues / discord

**BenchmarkFingerprint**(F7): 実行条件の同一性はモデルを含めない値で判定する。

```go
type FingerprintInput struct {
    Prompt                 []byte
    BenchmarkSchemaVersion string
    ContextSize            int
    RuntimeSignature       string // Runtime.Engine/Variant(比較条件に含む)
    TargetKind             string // local | sandbox
    ControllerVersion      string
}
func Fingerprint(in FingerprintInput) string // canonical JSON → sha256
// milestone 5 までに: recipe に generation フィールド(temperature・seed 等の生成条件)を追加し、
// canonical 化した上で FingerprintInput に含める。argv の中にしかない生成パラメータは
// 条件の違う 2 run を比較可能にしてしまうため、明示フィールドへ移す
```

- A/B/tie 投票の受理条件: **両 run の fingerprint 一致**。モデル digest は**記録値**であり一致条件ではない(モデル比較こそ本ベンチの目的)
- ただし **baseline と candidate が同一モデル ID の場合は両 run のモデル tree digest 一致を追加要求**(比較期間中の無音のモデル差し替え検出)

フロー: `review request` は両 run が succeeded + `public_url` 持ち + fingerprint 条件を検証してから、Issue へ marker 付きコメント(`<!-- llmbench:review:<id> -->` + URL ペア)。投票は marker コメントとして記録され、**正は Issue**、filestore は索引。`bot_login` 以外のコメントは解釈しない。`Issues` / `Notifier` interface をここで宣言。

- `issues`: go-github で `review.Issues` を実装
- `discord`: webhook へ Issue リンク 1 行 POST(`review.Notifier` を実装)

### 4.9 pages

`runner.Publisher` を実装。site repo の指定ブランチへ `runs/<run-id>/index.html` を commit(決定論的パスで冪等)。`ExtraFiles`(例: `_headers`)も同一 commit に含める。

**デプロイ要件(オペレータ責務として文書化)**:

- LLM 生成 HTML は任意 JS を含むため、**ベンチ専用の origin** で公開し、認証 cookie 等を一切置かない
- CSP: LLM 生成 HTML は inline `<script type="module">` を含むのが普通であり hash 列挙は現実的でないため、**専用 origin + 資格情報なし**を前提に `script-src 'self' <許可 CDN 固定> 'unsafe-inline'` と `connect-src 'none'` 相当を許容する構成とする
- GitHub Pages はカスタムレスポンスヘッダを持たないため、CSP を要するなら Cloudflare Pages 等ヘッダ可のホストを選択する(Publisher 実装自体はホスト非依存: git commit のみ)
- **k8s モードでは `<output>`(Artifacts.Dir の親)を永続ボリュームに置くこと**。ConfigMap RunStore だけでは artifact は Pod 再作成で失われ、finalizing からの復旧が不能になる

### 4.10 filestore

`RunStore` / `LeaseStore` / review 索引のファイル実装。

- RunStore: tmp+rename の atomic 置換と **StoreVersion 検査**(内部で単調増加カウンタを "N" 文字列として持つ。読み出し時の Version と不一致なら `ErrVersionConflict`)
- LeaseStore: `O_EXCL` create で atomic 取得(同一 runID の再取得は冪等成功)。解放は lock 下で owner を比較してから削除。**削除対象が存在しない場合は成功**(NotFound = success)。owner 不一致なら削除せず `ErrLeaseBusy`
- `runs/<runID>/input/`(recipe snapshot)は Submit が書き込み(temp + atomic rename)、runner が読む

### 4.11 httpapi(chi)

- `POST /v1/runs` `{experiment, input_commit?}` → 202 `{run_id}`。実験が allowlist target を選んでいるかは operator 設定で検証
- **local target への HTTP 経由 submit は、operator が `AllowHTTPLocal: true` を明示した場合のみ許可(既定拒否)**。`LLMBENCH_API_TOKEN` 未設定時は loopback bind のみとし、local target は常に拒否
- `GET /v1/runs/{id}` → Run 全体(Phase/HookState/LeaseState/ExecutionState/Artifacts/PublicURL)
- bearer 認証: `LLMBENCH_API_TOKEN` 設定時のみ要求
- handler はデコード → `run.Service`(Submit/Status interface をここで宣言)呼び出しのみ

---

## 5. CLI 仕様

```
llmbench validate <experiment.yaml>
llmbench submit <experiment.yaml> [--commit <full-sha>]     # Engine.Drain で同期駆動
llmbench status <run-id>
llmbench serve --config server.yaml [--state .state] [--output runs]
                [--retry-interval 30s] [--coordination-ns <ns>] [--lease-name <name>]
                [--kubeconfig <path>]
llmbench sandbox --namespace <ns> acquire <run-id> <warm-pool>
                | run <claim> '<sh-command>' | pull <claim> <src> <dst> | release <run-id>
llmbench review request <baseline-run-id> <candidate-run-id> --issue <n>
        | vote <review-id> --choice A|B|tie|invalid [--notes ...]
        | status <review-id>
```

環境変数: `LLMBENCH_API_TOKEN`、`LLMBENCH_GITHUB_TOKEN`、`LLMBENCH_DISCORD_WEBHOOK`。

## 6. 実装フェーズと完了条件

| # | 内容 | 完了条件 |
|---|------|---------|
| 1 | 骨格縦断スライス | experiment/operator(順序・limits 検証含む)/run(状態機械+Dispatcher+worker+TargetLease・実行 write-ahead)/runner(local, recipe snapshot 使用)/filestore/exec hook + validate/submit/status。`-race` 緑。write-ahead 順序・Acquire 失敗が共通 releasing に合流すること・lease busy 待ち・invoking 割り込み→failure・lease 解放後 terminal の単体テスト |
| 2 | serve | httpapi(local target 既定拒否を含む)+ 並列 dispatch + Recover + provenance 記録 |
| 3 | Sandbox 経路 | sandbox クライアント(Start/Stop 冪等、quote テスト)+ git 検証(snapshot vs commit)+ モデル digest + pin + claim hook + 成果物確定保存 |
| 4 | GitOps hook | §4.6 決定テーブルの全分岐 + 「マージ直後クラッシュ」再開(fake clientset + go-github fake) |
| 5 | pages + review | 冪等公開・`_headers`・fingerprint 検証・marker 投票・discord |
| 6 | kube 永続化 | ConfigMap store(不透明 StoreVersion)・条件付き claim 削除・Lease(喪失時 worker cancel) |
| 7 | 仕上げ | AGENTS.md / README / usage.md 更新、chromedp 除去、全テスト緑 |

各フェーズで `go test -race ./...` と `go vet ./...`。実データで実験プレースホルダを書き換えない。

## 7. 既存 docs からの意図的な変更点

1. **PNG preview 廃止**: chromedp・preview エンドポイント・browser sidecar を廃止し、Pages 公開で生 HTML を直接比較する。`review request` の前提は「両 run の succeeded + 公開済み」
2. **visual ブロック廃止**。同一条件は BenchmarkFingerprint で担保
3. **同一モデル digest 要求の限定**(§4.8): cross-model 比較を許すため「無条件の一致要求」から「同モデル時のみ」へ
4. **Finalize の後ろ倒し**(§3.1): 公開は GPU 解放後。**失敗 run は finalizing を経由せず failed**
5. **local target の位置づけ明確化**(§1.3, §4.11): 隔離境界ではない。HTTP 経由 local 実行は既定拒否
6. `docs/usage.md` / `README.md` / `AGENTS.md` の該当記述はフェーズ 7 で更新

## 8. テスト方針

- table-driven + fake(mock ライブラリ不使用)。`run` Engine は fake Hook/Store/HookSource/LeaseStore/Executor で:
  - write-ahead 順序(acquiring/invoking/releasing 保存→外部呼び出し→完了保存)の検証
  - lease: busy 待ち→解放後の自動前進、**条件付き解放(owner 不一致で削除しない・NotFound=成功)**、取得→保存間クラッシュの再開、解放→terminal の順序
  - **Acquire 失敗後の共通 releasing で acquiring hook も解放されること(not_started は解放しない)**
  - **Release の任意 error で released にせず・TargetLease を解放せず・terminal に進まないこと(ErrPending は待機分類)**
  - **ExecutionState: invoking からの復帰が再実行せず failure になること・completed+running は状態破損扱い**
  - **Execute 失敗 run が finalizing を経由せず failed になること**
  - **AcquireTargetLease の非 busy error で failure→releasing となること(冪等解放で曖昧成功を回収)**
  - finalizing: 公開失敗で停留→成功で succeeded
  - クラッシュ再開(各 Phase 中断点から)、**recipe snapshot 使用の検証(submit 後に recipe を書き換えても影響しない)**、HookPlanDigest 検証
  - 同一 target 排他・異 target 並行(`-race`)
  - **limits: MaxExecutionDuration での Execute 打ち切り**
- k8s は fake clientset、GitHub は go-github fake/httptest、exec hook は実 exit 75 スクリプト
- `provenance` は golden digest と symlink 拒否。`sandbox` の quote は危険文字テーブル
- `filestore` は書き込み中断シミュレーションと StoreVersion 競合テスト
- `gitops` は §4.6 決定テーブルの全分岐(**drift ケース: 自 PR merged なのに manifest が逆方向、を含む**)
- `operator` は hook 順序不変条件違反の検出テスト

## 9. コーディング規約

- エラーは sentinel(`run.ErrPending` / `run.ErrLeaseBusy` / `run.ErrVersionConflict`)+ `%w`。分岐は `errors.Is`
- context 第一引数。`Clock` 注入。ID 生成は注入可能
- 設定は yaml KnownFields で未知フィールド拒否。canonical JSON はソートキーで決定論化
- 決定論的外部名: Claim `llmbench-<runID>`、プロセス handle `runtime-<runID>`、ブランチ `llmbench/{pause,restore}-<runID>`、公開 `runs/<runID>/`、成果物 `<output>/runs/<runID>/`(input snapshot はその下 `input/`)
- パッケージコメント必須。パッケージ名は提供物を語る
- ログは logr 構造化フィールド

## 10. 実装時に確認すべき未検証事項・将来拡張

1. agent-sandbox SDK v1.0.4 の sandboxd が argv exec を直接受けられるか、長寿命プロセス primitive を扱えるか。不可なら §4.3 の quote 境界実装+決定論的タグによるバックグラウンド起動。マイルストーン 3 冒頭で確認
2. `git archive` の出力形式と Sandbox 側展開(tar)の整合。マイルストーン 3 でスモーク
3. Cloudflare Pages `_headers` での CSP 構成の挙動。マイルストーン 5 で確認
4. (将来拡張)invoke を managed process(`invoke-<runID>`)+ completion receipt にして、`invoking` 割り込みからの再アタッチを可能にする。v1 は「interrupted = failure」で足りる

## 11. 変更履歴

- v1.5.2: 実装フィードバックの反映 — `internal/hook`(コマンドフック)を構成に追加、fsync の失敗は握り潰さず snapshot 書き込み自体を失敗させる(設計の「確定保存」を実行時に担保)、公開は静的サイト(専用 origin + CSP)で行いスクリーンショットは持たないことを明記
- v1.5.1: OK 判定時に指摘された後続対応事項を設計に先折り込み — ProcessHandle を string に、ExpectedFile の所有を provenance へ、ErrVersionConflict 時の worker 動作(再 LoadRun・外部効果禁止)を明記、BuildHookPlan(t, runID)、Drain(ctx, runID)、旧 rollback 表現の削除
- v1.5: 外部レビュー 5 周目の反映 — **blocker**: ① PlannedHook/GitOpsPlan/SandboxPlan を operator パッケージへ移動し operator.BuildHookPlan の戻り型との依存循環を解消(run → operator のみ)、② Release のエラー意味論を一本化(ErrPending 以外の error も released にせず・TargetLease を解放せず・terminal に進まない。ErrPending は UI 分類。ReleaseTargetLease も同様)、AcquireTargetLease の非 busy error は failure→releasing(冪等解放で回収)。**非 blocker**: SandboxClient の用語統一(EnsureSandboxClaim)、Git.VerifyCommit を期待 SHA256 明示型(expected []ExpectedFile)へ変更、テスト方針の旧 rollback 表現を修正
- v1.4: 外部レビュー 4 周目の反映 — **blocker**: ① Acquire 失敗の rollback を専用経路から廃止し共通 releasing に合流(解放対象 = acquiring/acquired/releasing、not_started は解放しない)、② TargetLease 解放契約に「存在しない場合 = 成功(NotFound = success)」を明記し完全冪等化、③ invoking 復帰の制御フローを明示(continue で再 Execute を構造的に防止)+ completed+running の状態破損扱い。**非 blocker**: GitOps 決定テーブルに drift ケース(自 PR merged なのに manifest が逆方向 = 永続エラー)を追加、BuildHookPlan を順序決定の唯一の箇所として定義、Submit の snapshot 保存順を明示(temp → atomic rename → SaveRun、孤立 dir は GC)、用語を TargetLease / SandboxClaim に分離(ClaimState→LeaseState、ClaimStore→LeaseStore)、Fingerprint に RuntimeSignature を追加し milestone 5 で generation 条件フィールドを計画
- v1.3: 外部レビュー 3 周目の反映 — **blocker**: ExecutionState write-ahead の追加(invoking からの復帰は自動再実行せず failure)、claim 解放側の write-ahead(ClaimState=releasing)+ReleaseClaim の owner 条件付き削除契約、recipe snapshot(RecipeJSON・PromptSHA256・input/ ファイル確定保存、runner は snapshot のみ使用)、Execute 失敗 run は finalizing を経由せず failed+FinalizeResult 型の導入+PublishError フィールド化、StoreVersion を不透明文字列に(resourceVersion を数値化しない)、GitOps→Sandbox 順序の設計不変条件化(operator.Load が強制)、operator 側 max_ready/execution_duration の導入、local runner は隔離境界ではないことの明記+HTTP 経由 local 実行の既定拒否。**非 blocker**: FingerprintInput 構造体+canonical serialization、PlannedHook の型付き・バージョン付き化、HookPlanDigest の毎回検証、Artifacts.Dir の永続ボリューム要件、Lease 喪失時の worker context cancel
- v1.2: target claim の write-ahead 化、rollback 対象に最後の acquiring hook を追加、finalizing の一本化、runtime.start の長寿命プロセス化、Artifacts.Dir 決定論化、HookPlan snapshot、Run.Revision(→v1.3 で StoreVersion に改訂)、CSP 現実的構成
- v1.1: 状態機械の再構成、write-ahead、HookSource、並列 dispatch、Finalize の GPU 解放後移動、BenchmarkFingerprint、Pages 隔離要件、argv 境界、gitops 帰属判定
- v1.0: 初版
