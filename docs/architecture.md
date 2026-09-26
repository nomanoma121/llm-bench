# llm-bench コントローラ実装設計書(v1.6.0)

> この文書は `docs/design.md`(ドメイン要件)・`docs/usage.md`(機能仕様)・`AGENTS.md`(規約)を実装に落とすための設計 blueprint である。
> ChatGPT 等の外部レビューに単体で渡せるよう、背景要件から実装方針までを自己完結して記述する。
> ステータス: v1.6.0 — v1.5.3 の実装(main マージ済み)に対し、**公開責務を分離**した。恒久公開は「人間が採用した artifact を main にマージしたこと」だけをトリガーに CI が行い、controller は認証付き候補プレビュー(§4.9)のみを提供する。controller 側 publication(旧 pages / `finalizing` フェーズ)は §4.12 の CD に置き換えて削除する。実装状況は §2.5、v1.6 の変更は §11。

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
experiments/<model-id>/<exp-id>/   # config.yaml(recipe) + README.md(仮説・考察)
                                   #   + output/(採用した index.html と manifest.json。§4.12)
charts/llmbench/                   # 実験的 Helm Chart(values-only scaffold)
cmd/llmbench/                      # Go コントローラ(本設計の実装対象)
.github/workflows/pages.yml        # main マージで experiments/ から静的サイトをビルドし Pages へ deploy(§4.12)
```

### 1.2 実行ワークフロー(要件の核心)

1. 明示的なリクエスト(CLI/HTTP)だけが run を開始する。Issue や PR の作成・編集は run を消費しない
2. 推論ワークロードの停止は **GitOps**: **ベンチリポジトリとは別のマニフェストリポジトリ**(オペレータ設定の `owner`/`repository`/`base_branch`)の特定 YAML スカラーを `active_value` → `paused_value` に変える PR をコントローラが作り、**人間がマージ**する。Argo CD 同期と推論 Pod 停止を確認してから GPU を使う
3. GPU 実行は Agent Sandbox(k8s CRD + Pod port-forward の sandboxd)内で行う。Claim 名は run ID から決定論的に導出される
4. **取得順の設計不変条件: 推論 Pod の停止確認(GitOps pause 完了)→ GPU Sandbox claim 取得。解放は厳密に逆順: Sandbox プロセス停止+claim 解放 → GitOps restore → …。この順序は operator 設定に対する検証ルールとして機械的に強制する**(§4.2, §4.6)
5. 実行後は成果物を Sandbox 外へ保存し、Claim を解放し、**復帰 PR** を作る。**復元は実行が失敗しても必ず行う**。target claim の解放が完了するまで run を terminal にしない
6. 成果物は Sandbox 外へ保存し hash を記録する。**run の成功は公開を意味しない**。controller は認証付き候補プレビュー(§4.9)を提供し、Issue に baseline/candidate を投稿、人間が A/B/tie/invalid で投票。Discord は Issue へのリンク通知のみ
7. 1 target で同時実行は不可(**submit は可能。後続 run は claim 解放待ちとして待機**)。**異なる target は並行実行可**
8. **恒久公開の唯一のゲートは「人間が採用した artifact を `experiments/.../output/` に固定して main にマージしたこと」**(§4.12)。controller は公開先への書き込み権限を持たない

### 1.3 コントローラ境界(権限分離)

- 実験 YAML(`experiments/.../config.yaml`)が指定できるのは model / benchmark / **allowlist 済 target ID** / runtime 設定(start・invoke を含む recipe)/ context のみ。**実験 YAML に特権設定(GitOps パス・GPU・認証情報・公開先・タイムアウト上限)は書けない**
- target の実体(GitOps 対象、Sandbox pool、hook コマンド、プレビュー配信の外向け URL、レビュー設定、**実行時間の上限値**)は**オペレータ設定**(別 YAML、CLI フラグで指定)のみに存在する
- 認証情報は環境変数または k8s Secret のみ。Sandbox Pod と実験コマンドのプロセスに認証情報を渡さない
- SSH 実行経路は存在しない
- **local target は信頼された開発用途専用であり、Sandbox と同等のセキュリティ境界を提供しない**(os/exec の子プロセスはコントローラと同一の filesystem/network 権限を持つ)。HTTP 経由での local target 実行は**デフォルト禁止**(§4.11)

### 1.4 機能要件一覧(実装スコープ)

| # | 要件 | 出典 |
|---|------|------|
| F1 | 実験 recipe のパース・バリデーション | usage.md |
| F2 | オペレータ設定(target allowlist / hooks / gitops / sandbox / preview / review / **limits**)のパース・バリデーション。**hook 順序不変条件の検証を含む** | usage.md |
| F3 | run 状態機械 + 順序付き acquire/release hook + 冪等リカバリ(**claim・実行状態を含む全外部効果の write-ahead**) | design.md |
| F4 | コマンドフック: 引数配列を shell なしで実行、**exit 75 = acquisition pending** | usage.md |
| F5 | ローカル実行ランナー(argv 実行、`LLMBENCH_*` 環境変数は allowlist 型で注入、認証情報は継承しない) | usage.md |
| F6 | Sandbox 実行ランナー: コミット検証 → archive アップロード → **長寿命プロセスとして `runtime.start`**(ready 確認)→ `invoke` → 成果物回収。**recipe は submit 時 snapshot を使用** | design.md |
| F7 | 来歴記録: BenchmarkFingerprint・成果物 hash(Sandbox 外保存の瞬間に計算)・モデル tree digest(symlink/欠落拒否、オペレータ pin と照合) | design.md |
| F8 | 1 target 1 実行の atomic claim。**取得・解放とも write-ahead で、解放は owner 条件付き削除。解放完了前に terminal にしない** | design.md |
| F9 | 永続化: ローカル file store(単プロセス)/ k8s ConfigMap store(**不透明な Version 文字列による CAS**)+ Lease リーダー選出 | usage.md |
| F10 | HTTP API: run 投稿・状態参照・bearer 認証(**token 未設定時は loopback bind のみ**) | usage.md |
| F11 | CLI: validate / submit / status / serve / sandbox / review / adopt / adopted verify / site build | usage.md |
| F12 | GitOps hook: 状態と帰属を考慮した冪等な pause/restore PR + Argo CD `Synced` 確認 + Deployment/Pod 状態確認(§4.6) | usage.md |
| F13 | Artifact プレビュー: control API とは**別リスナ**で GET/HEAD のみ配信。**配信 root は output/ に限定**し、CSP sandbox・nosniff・path confinement(`os.OpenRoot`)を強制。**run 成功 ≠ 公開** | 本設計で新設 |
| F14 | Issue A/B レビュー: marker 付きコメントの機械解釈(bot_login 制限)、投票履歴の正は Issue。前提は「両 run が succeeded + artifact 確定(`ArtifactDigest` 記録済み)」。marker には **baseline/candidate それぞれの run ID と artifact digest のペア**を残す。比較条件は BenchmarkFingerprint、**同モデル比較時のみモデル digest 一致を追加要求** | usage.md |
| F15 | 再起動リカバリ + 並列 dispatch: run 毎 worker、異なる target の並行実行、30 秒周期の dispatch tick | usage.md |
| F16 | **実験 recipe・prompt の submit 時 snapshot**: run の再開は常に snapshot を使い、生きたリポジトリファイルに依存しない | 本設計 |
| F17 | **operator 側の強制タイムアウト上限**: 実験者が引き上げられない ready/実行時間の上限を target 設定に持たせ、recipe 側 timeout はその範囲内でのみ許可 | 本設計 |
| F18 | 採用と恒久公開: `adopt`(PV の artifact を git へ固定、`ArtifactDigest` 照合・`--into` confinement・review 完了述語・single-file 契約)・`manifest.json`・`adopted verify`・`site build`・main merge をトリガーとする CI 公開。**controller から site repo への書き込み経路を持たない** | 本設計で新設 |

### 1.5 非機能要件

- `go test -race ./...` と `go vet ./...` が常時緑。ポリシーロジックは外部 SDK なしでテスト可能
- 外部効果はすべて冪等。プロセスが任意の時点で死んでも再起動後に収束する(**外部呼び出し前に意図と進行状態を永続化する write-ahead 原則** — claim・hook・実行・プロセスのすべてに適用)。**結果の再現が不可能な処理(ベンチ実行本体)は、write-ahead からの復帰時に自動再実行せず失敗扱いにする**
- **run の成功条件に公開を含めない**。恒久公開の唯一のゲートは「人間が採用し main にマージしたこと」であり、controller は公開の成否を知らない
- 実験 YAML を変えずにオペレータ設定だけで実行先を差し替え可能
- 依存は go.mod 固定分のみ。新規依存の追加は避ける

### 1.6 スコープ外

- Agent 最適化ループ(design.md が「未接続」と明記。将来 `runner` と同型の executor として接続する想定)
- モデルダウンロード(models.yaml の解釈はコントローラ外)
- invoke プロセスへの再アタッチ+completion receipt による実行再開(v1 は「interrupted = failure」を採用。将来拡張として §10 に記載)
- リアルクラスタでの e2e 検証、マルチレプリカ fence、Helm Chart の本格整備
- プレビューの外部公開(Cloudflare Access / Workers 等)と SSG(Astro 等)の本格導入。v1.6 は「Ingress + クラスタ認証のプレビュー」と「Go の `site build` + Actions の最小公開サイト」で開始する(§4.12)

---

## 2. 全体アーキテクチャ(Go 慣習版)

### 2.1 採用するスタイルと理由

**レイヤー名のディレクトリ(domain/usecase/repository/handler)は作らない。** 機能軸でパッケージを切り、interface は消費者側に小さく定義する。理由:

- 本リポジトリの AGENTS.md が既に「Cobra コマンドは `cmd/llmbench`」「HTTP は薄い `internal/httpapi`」「外部効果は注入可能に」「**小さい interface は使用点で定義**」と規定しており、Go 慣習版がそのまま整合する
- interface を port 専用パッケージに集約すると、実装との対応が間接化され、Go の暗黙的 interface 満足の利点(実装側は抽象を知らない)が薄れる

### 2.2 設計原則

1. **依存は一方向・循環禁止**: `cmd → {httpapi, 機能パッケージ}`、`httpapi → run(ポリシー)`、機能パッケージ同士は縦方向のみ
2. **interface は消費者側で宣言**: `run` が `Hook`/`HookSource`/`Executor`/`RunStore`/`LeaseStore` を、`review` が `Issues`/`Notifier`/`PreviewURLResolver` を、`runner` が `SandboxClient`/`Git` を、`httpapi` が preview 用の `ArtifactStore` を宣言する。実装パッケージはそれらを**暗黙に満たす**(抽象を import しない)
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
  adopt.go             # llmbench adopt --into experiments/<model>/<exp>
  site.go              # llmbench adopted verify / site build(CI と共有)
  wire.go              # ★配線: 実装の構築と注入(HookSource 含む)を集約

internal/
  experiment/          # 実験 recipe。型・パース・バリデーション・canonical serialization(純粋)
  operator/            # オペレータ設定。型・パース・バリデーション(順序不変条件・limits 検証を含む)
  run/                 # ★中核: Run 型・状態遷移・Engine(Dispatcher+worker)。
                       #   Hook/HookSource/Executor/RunStore/LeaseStore をここで定義
  runner/              # ベンチ実行の具体化。local.go / sandbox.go
                       #   SandboxClient/Git interface をここで定義
  provenance/          # 来歴: sha256・BenchmarkFingerprint・モデル tree digest・
                       #   model-identity.json・git スナップショット検証(git バイナリを exec)
  sandbox/             # agent-sandbox SDK の薄いクライアント(SDK import 可)
  gitops/              # GitOps pause/restore hook(go-github + kube を使用)
  kube/                # k8s 全般: ConfigMap store・TargetLease store・Lease・Argo CD/Deployment/Pod 確認
  issues/              # GitHub Issue コメント操作(review.Issues を実装)
  review/              # A/B レビューのポリシー(marker 検証・投票・記録)
  discord/             # webhook 通知(review.Notifier を実装)
  hook/                # コマンドフック(operator計画→run.Hook、exit 75→ErrPending)
  filestore/           # ローカル file 実装(RunStore/ReviewStore/LeaseStore/ArtifactStore)
  adopt/               # 採用: PV の artifact を git working tree へ固定、manifest 生成/検証(§4.12)
  sitebuild/           # 静的サイト生成(manifest を持つ実験のみ。ネットワーク不要。§4.12)
  httpapi/             # chi HTTP サーバ(薄い)。認証・デコード → run の Service へ委譲
                       #   preview(§4.9)は別リスナの別ハンドラ
```

### 2.4 依存方向

```
cmd/llmbench ──▶ 全パッケージ(配線)

httpapi ──▶ run
runner ──▶ run, provenance, experiment, operator
review ──▶ run(型参照のみ)
run ──▶ experiment, operator(型のみ), logr
provenance ──▶ (なし。標準ライブラリ + git バイナリ)

sandbox ──▶ run(契約型のみ), (agent-sandbox SDK)
gitops ──▶ run(契約型のみ), kube, (go-github)
kube ──▶ run(契約型のみ), (client-go)
hook ──▶ run(契約型のみ)
issues ──▶ (go-github)
discord ──▶ (net/http)
filestore ──▶ run(契約型・sentinel のみ), (標準ライブラリ)
adopt, sitebuild ──▶ provenance(検証は 1 箇所)
```

- **run-wide の phase/state-transition ポリシーは `internal/run` にしか置かない**。一方で interface 実装(`filestore`/`kube`/`hook`/`sandbox`/`gitops`)は **`internal/run` の契約型と sentinel(`run.Run`/`run.ErrVersionConflict`/`run.ErrLeaseBusy`/`run.ErrPending` 等)を import してよい**(AGENTS.md と同じ規則)。実装→抽象の import はこの契約参照に限られ、暗黙的型満足のために interface 自体を import する必要はない。統合固有の reconciliation/ownership 判断(GitOps の決定テーブル、command hook の exit 75 解釈)は各実装パッケージが持つ
- `gitops → kube` は同一インテグレーション層内の参照として許容
- `runner` は SDK を import しない。`SandboxClient` / `Git` の interface を満たす実装を cmd が注入する。preview(§4.9)は `httpapi` が artifact ディレクトリだけを受け取る(read-only)

### 2.5 実装状況

この設計に対する実装は、設計書に続いて以下の単位で main に積み上げた:

| 単位 | 内容 |
|------|------|
| run state machine | `experiment` / `operator` / `run`(write-ahead 状態機械+Engine)/ `runner`(local)/ `hook` / `filestore` / `provenance`(fingerprint)、CLI `validate`/`submit`/`status` |
| serve + HTTP API | `httpapi`(認証・公開URL)/ dispatcher・graceful shutdown・loopback 制約・repository-relative path 強制・local target の HTTP 既定拒否 |
| Sandbox runner | `sandbox`(決定論的 claim・Ready/削除の収束待ち・argv境界quote・detachプロセス)/ `runner.Sandbox`(commit照合・archive転送・readiness clamp・モデル digest pin) |
| GitOps hook | `gitops`(状態+帰属の決定テーブル・同一revisionでのArgo収束・Pod消滅確認・rollback・drift拒否)/ `kube`(Argo CD・Deployment/Pod確認) |
| レビュー | `issues`(bot_loginでfail-closed)/ `discord`(Issueリンクのみ)/ `review`(Issueが投票履歴の正、fingerprint検証) |
| 旧 publication(v1.5) | `pages`(非force・競合リトライで冪等公開)/ `runner.PublishFinalizer` / `finalizing` フェーズ。**v1.6 で削除**(§4.12 の CD へ移行) |
| Kubernetes 協調 | `kube`(ConfigMap store=CAS、TargetLease=owner条件付き削除、Lease リーダー選出と喪失時の worker cancel、再選出) |

v1.6 で追加する単位(§6 のフェーズ 8〜11): `provenance.ArtifactDigest` と Run への digest/`ControllerVersion` 記録、preview API(§4.9)、adopt + manifest + verify、`site build` + Actions(§4.12)、旧 publication の削除(`finalizing` の legacy 移行を含む)。**v1.6 の設計はこの文書が正であり、実装は未着手**(利用者向け docs と `examples/` は実装フェーズで更新する)。

未検証・未実装: 実クラスタでの e2e(Argo CD / Deployment / SandboxClaim の遷移)、Ingress + クラスタ認証でのプレビュー配信、マルチレプリカでの外部効果 fence の実証、Agent 最適化ループの接続(attempt 境界は §4.12 と §10 に設計のみ)、Git LFS の materialize。

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
Phase:  pending ──▶ acquiring ──▶ running ──▶ releasing ──▶ succeeded or failed
              claim取得+          │ ExecutionState       │ release失敗
              hook順次acquire     │  write-ahead         │ → releasing に留まり
                          │       │                      │   worker が interval リトライ
                          │ ErrPending /                 │
                          │ claim busy は同 phase         │
                          │ で interval リトライ          │
                          ▼                              ▼
                 (クラッシュ時は保存済み状態から冪等に再開)
```

- `Phase = pending | acquiring | running | releasing | succeeded | failed`
- **`finalizing` は v1.6 で廃止**した(公開は run の成功条件ではない)。v1.5.x の record が `finalizing` で停留している場合のみ、worker が `succeeded` へ移行する(legacy 移行。§3.3)
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
- **releasing 完了後の分岐は ExecutionResult で決定**: `failure → failed`、`success → succeeded`。**公開の成否は run の終了条件に含めない**(§1.5, §4.12)
- 1 target の同時実行は LeaseStore が排除する。submit 自体は常に可能で、後続 run は `acquiring` で待機する

### 3.2 主要な型と interface(run パッケージで定義)

```go
type Phase string // pending | acquiring | running | releasing | succeeded | failed (+ legacy: finalizing)
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
    // ArtifactDigest は payload(output/ 配下の全 regular file。§4.4 の canonical 規則)の tree digest。
    // **artifact 確定保存の瞬間に計算**し、この値が非空の間だけ preview(§4.9)・review(§4.8)・
    // adopt(§4.12)が artifact を「確定済み」として扱う。3 者は必ず同じ関数(provenance.ArtifactDigest)
    // を使う。digest が空 = 未確定(実行中・保存失敗)なので配信も adopt もしない
    ArtifactDigest    string
}

type Run struct {
    ID              string          // 32 hex
    Target          string
    Experiment      string          // リポジトリ相対パス(記録用)
    InputCommit     string          // Sandbox 実行では必須(40 hex)
    ControllerVersion string        // submit 時に実行中バイナリの version を永続化(adopt が manifest へ写す。§4.12)
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
    PublishError    string          // legacy(v1.5): 読取のみ。新規 run では書かない
    Artifacts       Artifacts
    PublicURL       string          // legacy(v1.5): 読取のみ。恒久公開 URL は CD が決める(§4.12)
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

type Engine struct { /* RunStore, LeaseStore, HookSource, Executor, Logger, Clock, RetryInterval */ }
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
    # lease released の保存後のみ分岐する(公開の成否は run の終了条件ではない):
    - ExecutionResult == failure → **failed**
    - ExecutionResult == success  → **succeeded**
  case finalizing:  # 旧 record の移行専用。新規 run はこの phase に入らない
    # v1.5.x の publication 待ち record のみ到達する。LeaseState=released かつ
    # ExecutionResult=success なので published 済みか否かに関わらず succeeded とする
    save(Phase=succeeded)
  case succeeded, failed: return
```

- **1 ステップ 1 保存**。任意の時点で死んでも、保存済み Phase/HookState/LeaseState/ExecutionState から同一ステップを冪等に再開する
- **SaveRun が ErrVersionConflict を返した場合**: ローカルの Run を破棄して LoadRun し直し、保存済み状態から再判定する。**競合した SaveRun に対応する外部効果は実行してはならない**(write-ahead の保存が完了する前に外部効果を起こさない原則の帰結)
- `ListUnfinished` は non-terminal を返す。旧 record の `finalizing` 停留 run は worker が `succeeded` へ移行する(外部効果を伴わないため write-ahead 不要)

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
    Preview *Preview
    Site    *Site   // deprecated(v1.5): フェーズ 8〜10 の移行期間のみ併存。フェーズ 11 で削除する
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

// Preview は候補 artifact の配信(§4.9)。controller は公開先を持たないため site repo の設定は存在しない。
// base_url は「Issue に貼る外向け URL」であり、listen address からは導出できない。
// フェーズ 11(旧 publication 削除)で site: を削除し、その時点から旧設定は
// 黙って無視せず明示エラーで fail させる。フェーズ 8〜10 は両方を併存させ、
// preview を有効にした設定でも旧 publication を残せる(段階移行)
type Preview struct {
    BaseURL string // 必須。絶対 URL(例: https://llmbench-preview.example.internal)。Ingress が認証を終端する前提
    // 配信制限は実装定数(file size 上限 = 8 MiB/ファイル、GET/HEAD のみ、listing なし)とし、
    // v1.6 では operator 設定にしない(変更要求が出たら設定化する)
}
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
// - Preview.BaseURL: 設定時は絶対 URL(http/https)であること。Review を有効にする場合は必須
// - フェーズ 11 以降: site: フィールドが現れた場合はエラー(KnownFields で拒否される)
func (c Config) ValidateRecipe(target string, readyTimeoutSeconds int) error // operator は experiment を import しない
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
  7. `output/index.html`・ログ・`model-identity.json` を回収し **Sandbox 外の `Artifacts.Dir` へ保存。sha256 はこの保存の瞬間に計算**する。この保存が release の前提条件
- **artifact は self-contained な単一 `output/index.html` に固定する**(v1.6)。local/sandbox とも **`output/` に `index.html` 以外の regular file があれば seal に失敗**させる(§4.12 の予約名 `manifest.json` と同じ扱い)。理由: 付随する JS/CSS/JSON を payload に含めると、preview の CSP(`connect-src 'none'`)と公開サイトの `srcdoc` 埋め込みでは**表示できない**(相対 URL の解決先が存在せず、`script-src` も preview origin を許可しない)。multi-file を認める場合は bundle/inline の仕様を別途定義する(§10)。回収は**単一ファイルの取得**であり、Sandbox 由来の bytes を信頼しないため **per-file 8 MiB 上限**を適用する(超過は失敗。§4.9 の配信上限と同じ定数)
  8. プロセス停止は release フェーズで行う(`Stop`): SandboxClaim の release hook が `Stop` → `ReleaseSandboxClaim` の順に実行
```go
// プロセスハンドルは不透明な文字列 ID(決定論的: "runtime-<runID>")。
// 構造体を runner で定義すると sandbox 実装が runner を import する必要が生じ
// 「実装→ポリシー import なし」原則を崩すため string で受ける
type ProcessHandle = string

type SandboxClient interface { // 実装: internal/sandbox。argv を受け取る
    // Acquire は Ready になるまで完了しない(未Readyは ready=false)。
    EnsureSandboxClaim(ctx, claimName, warmPool string) (ready bool, err error)
    // 長寿命プロセス管理。冪等: 生存していれば既存 handle を返す
    Start(ctx, runID, claimName string, argv []string, env map[string]string, cwd string) (handle string, err error)
    Stop(ctx, claimName, handle string) error // 冪等
    Exec(ctx, claimName string, argv []string, env map[string]string, cwd string) (stdout, stderr []byte, exitCode int, err error)
    Put(ctx, claimName string, r io.Reader, dest string) error
    Pull(ctx, claimName, path string) ([]byte, error)
    // Foreground 削除後に claim の消滅(= Pod/GPU のカスケード完了)を待つ。
    ReleaseSandboxClaim(ctx, claimName string) (released bool, err error)
}
// ExpectedFile は provenance パッケージで定義する(runner.Git が要求すると
// provenance 実装が runner を import する必要が生じ循環するため。runner → provenance のみ許可)
type ExpectedFile struct{ Path, SHA256 string } // snapshot 由来の期待値
type Git interface { // 実装: internal/provenance
    VerifyCommit(ctx, commit string, expected []ExpectedFile) error
    Archive(ctx, commit string) ([]byte, error) // tar ストリーム
}
```

sandboxd がシェル文字列しか受け付けない場合、**argv→シェル文字列の quote は `sandbox` パッケージ内部の境界実装に限定**し、quote 専用のテーブルテストで担保する。interface 契約は argv のまま。長寿命プロセスも SDK に session/process primitive があればそれを、無ければ決定論的タグ付きのバックグラウンド起動(`Exec` 経由 + pgrep 相当の生存確認)で実装する(§10.1)。

SandboxClaim のライフサイクルは hook として登録(`sandbox.Hook`: Acquire=EnsureSandboxClaim、Release=Stop+ReleaseSandboxClaim)。

### 4.4 provenance(来歴、git バイナリを exec)

- sha256 ヘルパー、`model-identity.json` 形式定義
- `HashTree(dir)`: 正則ファイルのみ。symlink・欠落はエラー。レコード = `path \0 size \0 sha256` をソート連結して tree digest。Sandbox 内では同アルゴリズムのスクリプト(python3)を投入し、共通の digest 計算で比較
- `ArtifactDigest(outputDir)`: **artifact 同一性の唯一の定義**(v1.6)。`outputDir` 配下の全 regular file を対象に、`HashTree` と同じ canonical 規則(`path` は outputDir からの相対・`/` 区切り、`size` は 10 進、`sha256` は小文字 hex)で計算する。**symlink はエラー**(たどらない)。**`manifest.json` は payload に含めない**(§4.12 の自己参照回避)。preview・review・adopt・`adopted verify` は必ずこの 1 関数を使い、独自の hash 規則を持たない

### 4.5 sandbox(agent-sandbox SDK)

SDK を包む薄いクライアント。`runner.SandboxClient`(Start/Stop/Exec/Put/Pull/Claim)と claim hook を提供。Claim 名・プロセス handle 名は run ID 決定論的 → 冪等。sandboxd は Pod port-forward で到達(Service 非公開)。**トランスポート断でもコマンドを自動再実行しない**(副作用があるため)。再実行は上位(worker)が write-ahead 状態に基づいて判断する。

### 4.6 gitops(状態+帰属を考慮した冪等 hook)

PR の宛先は**ベンチリポジトリとは別のマニフェストリポジトリ**(オペレータ設定 `targets.<id>.gitops` の `owner`/`repository`/`base_branch`)である。実行中のコントローラが持つ GitHub 権限は**マニフェストリポジトリの PR 作成**と**Issue コメント**だけで、ベンチリポジトリに対しては読み取りのみ、**公開サイト(publication host)への書き込み権限は一切持たない**(採用 artifact を git へ固定するのは人間が実行する `adopt` CLI であり、それはローカル作業ツリーに書くだけで認証情報を必要としない)。決定論的ブランチと PR はすべてマニフェストリポジトリ上に作成され、Argo CD Application はそのリポジトリを監視する。

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
- Argo CD は unstructured で単一 source のみ。**判定で manifest を読んだのと同じ snapshot revision(base SHA)** で `Synced` であること、Deployment/Pod の停止/`active_replicas` 到達を `kube` の関数で確認
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
    Generation             string // recipe の generation(temperature/seed 等)の canonical JSON
    TargetKind             string // local | sandbox
    ControllerVersion      string
}
func Fingerprint(in FingerprintInput) string // canonical JSON → sha256
// recipe の generation は実装済み: 未設定と明示 0 を区別するポインタ型で保持し、
// canonical JSON を fingerprint に含める(サンプリング条件だけ違う run は比較不能)。
```

- A/B/tie 投票の受理条件: **両 run の fingerprint 一致**。モデル digest は**記録値**であり一致条件ではない(モデル比較こそ本ベンチの目的)
- ただし **baseline と candidate が同一モデル ID の場合は両 run のモデル tree digest 一致を追加要求**(比較期間中の無音のモデル差し替え検出)

フロー: `review request` は両 run が succeeded + **artifact が確定(`Artifacts.ArtifactDigest` 非空)** + fingerprint 条件を検証し、**配信中の payload から `provenance.ArtifactDigest` を再計算して記録値と一致することを確認**してから、Issue へ marker 付きコメント(`<!-- llmbench:review:<id> -->` + preview URL ペア + **run ID と artifact digest**)を投稿する。投票は marker コメントとして記録され、**正は Issue**、filestore は索引。`bot_login` 以外のコメントは解釈しない。`Issues` / `Notifier` interface をここで宣言。

- review marker は `{baseline:{run_id,artifact_digest}, candidate:{run_id,artifact_digest}}` を含む canonical JSON として記録し、`adopt --review` はこれを Issue から読んで検証する(§4.12)
- **preview URL は Run に永続化しない**。operator 設定 `preview.base_url` から導出する実装(`type PreviewURLResolver interface{ URL(runID string) string }`)を review 側へ注入する(listen address から Ingress の URL は導出できない)
- 同一性の正は `run_id` + artifact digest。**URL が失効しても(Ingress 変更・PV 削除)、Issue 上のレビュー履歴は壊れない**
- **review 完了 = 有効な vote marker が 1 件以上あり、最新の解決結果が対象 run を選んでいる**(`tie`/`invalid` は未解決)。`adopt --review` はこの述語を Issue 正本で検証する(§4.12)
- レビューは従来どおり「GPU 解放・restore 完了後」を条件に含める(人間待ちで GPU を保持しない)。preview 自体は artifact 確定直後から見える
- 社外レビュアに開かせる要件が出た場合はこの方式だけでは成立しない(Cloudflare Access 等の外部 ID 許可か一時 deployment が必要)。**内部 DNS 名を Issue に貼るのは避ける**

- `issues`: go-github で `review.Issues` を実装
- `discord`: webhook へ Issue リンク 1 行 POST(`review.Notifier` を実装)

### 4.9 preview(候補 artifact の配信)

**責務**: controller は「候補を生成し immutable artifact として保存し、安全な一時プレビューを提供する」までを担う。**恒久公開は行わない**(§4.12)。

- 経路: `GET|HEAD /v1/runs/{id}/artifacts/{path...}`。実体は `<output>/runs/<runID>/output/` 配下のみ。`input/`(recipe snapshot)や output 外のログは配信しない
- **seal された artifact だけを配信する**(2 段の防御):
  1. **seal の実装契約(durability を含む厳密な順序)**: runner は `<output>/runs/<runID>/output/` を直接書かない。**同一ファイルシステム上の sibling temp dir** に全ファイルを書き → **各ファイルを fsync** → **temp dir 自体を fsync** → **`rename(temp, output)`** → **親ディレクトリ(`<output>/runs/<runID>/`)を fsync** → その後に `Artifacts{ArtifactDigest: …}` を返し Engine が Run へ CAS 保存する。**親 dir の fsync 完了前に `ArtifactDigest` を永続化してはならない**(rename の directory entry が durable でないのに `ArtifactDigest != ""` になると「digest 非空 = sealed」契約が壊れる)。seal 後は誰もそのディレクトリを書き換えない(書き換える実装は契約違反)
  2. **配信時の検証(TOCTOU を閉じる)**: Run record の `Artifacts.ArtifactDigest` が非空であることを必須条件(空 = `invoking` 中または保存失敗 → 404)とする。さらに **①要求されたファイルをメモリへ読み込み、② payload 全体の `ArtifactDigest` を「①の bytes を使って」再計算し、③記録値と一致したときだけ①の buffer を返す**。不一致なら配信せず 409 を返す(ディスク上の改変・部分書き込み・外部からの差し替えを検出する)。**返した bytes は必ず検証済み digest に含まれる**ため、検証後〜read の間に差し替えられても未検証の bytes は返らない。1 ファイル 8 MiB 上限により buffer は有界。review/adopt も同じ照合を行う(§4.8/§4.12)。**1 リクエスト 1 ファイル**(listing なし)なので、この方式で応答全体が検証対象になる
  - これにより **Issue marker の digest とレビュアが見た bytes が一致する**ことが構造的に保証される
- **control API とは別リスナ**(`serve --preview-addr`)。control API の bearer 認証は使わず、**上流(Ingress + クラスタ認証)での認証終端を前提**とする。非 loopback bind には「上流で認証する」ことを示す明示フラグを要求する(control API の loopback 規則と同型)
- 外向け URL は operator 設定 `preview.base_url` から導出する。**Run に `preview_url` を永続化しない**(URL は導出値。同一性の正は run ID + artifact digest。§4.8)
- **GET/HEAD のみ。CORS を有効化しない。directory listing 禁止。symlink 拒否。URL decode 後に path を検査**し、`..`・絶対パス・backslash・NUL を拒否する。root confinement は `os.OpenRoot` 系で行う(手書きの prefix 比較は使わない)
- **file size 上限は実装定数(1 ファイル 8 MiB)**とし、v1.6 では operator 設定にしない。超過は 413。artifact は単一 `index.html` 契約(§4.3)なので、実質 payload 全体がこの上限に収まる
- **レスポンスヘッダ(必須)**: `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' <許可 CDN 固定>; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; form-action 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; worker-src 'none'`、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`、`Referrer-Policy: no-referrer`。**`allow-same-origin` は付けない**(opaque origin 化)
- **脅威モデル(保証範囲を明示する)**: この構成で**保証する**のは「プログラム的通信と資格情報へのアクセスを遮断すること」= `fetch`/XHR/WebSocket/EventSource/`sendBeacon`(`connect-src 'none'`)、form 送信(`form-action 'none'`)、`object`/`frame`、worker、および opaque origin 化による cookie/localStorage 等へのアクセス拒否。**保証しない**のは「ブラウザが発する一切のネットワーク要求を止めること」で、**sandboxed frame 自身への navigation(`location.href = …`)は依然として HTTP 要求を発生させられる**(CSP `sandbox` の top-navigation 制約は standalone document では意味を持たず、`navigate-to` は現行ブラウザで信頼できない)。したがって:
  - **preview の origin には ambient credential を置かない**(認証 cookie を持たせない。認証は Ingress の OIDC で行い、preview origin 自体を資格情報のあるアプリと同一 origin にしない)
  - **preview から到達できる先を信頼済みサービスだけにする**(preview のリクエストは untrusted なブラウザから来るものとして扱い、ambient auth で保護された内部 API を同一ネットワークに置かない)
  - 「一切のブラウザ発ネットワークを禁止する」完全な保証が必要なら、**ネットワーク隔離した remote browser/runtime で描画する境界が必要**であり、v1.6 のスコープ外(§10)
- 補足: OIDC は「誰が閲覧できるか」を守るだけで、**閲覧者のブラウザを生成 HTML から守らない**
- preview handler は **SandboxClient に依存しない**。`RunStore` + artifact ディレクトリだけを見る(**Sandbox が消えていても配信できる**)
- プレビューは恒久公開ではない。artifact が確定した直後から見えてよい

**k8s モードでは `<output>`(Artifacts.Dir の親)を永続ボリュームに置くこと**。ConfigMap RunStore だけでは artifact は Pod 再作成で失われ、プレビューも adopt(§4.12)も不能になる

### 4.10 filestore

`RunStore` / `LeaseStore` / review 索引のファイル実装。

- RunStore: tmp+rename の atomic 置換と **StoreVersion 検査**(内部で単調増加カウンタを "N" 文字列として持つ。読み出し時の Version と不一致なら `ErrVersionConflict`)
- LeaseStore: `O_EXCL` create で atomic 取得(同一 runID の再取得は冪等成功)。解放は lock 下で owner を比較してから削除。**削除対象が存在しない場合は成功**(NotFound = success)。owner 不一致なら削除せず `ErrLeaseBusy`
- `runs/<runID>/input/`(recipe snapshot)は Submit が書き込み(temp + atomic rename)、runner が読む

### 4.11 httpapi(chi)

- `POST /v1/runs` `{experiment, input_commit?}` → 202 `{run_id}`。実験が allowlist target を選んでいるかは operator 設定で検証
- `GET|HEAD /v1/runs/{id}/artifacts/{path...}` は **control API ではなく別リスナ**(§4.9)。ここには bearer 認証を掛けず、Ingress 側で認証する
- **local target への HTTP 経由 submit は、operator が `AllowHTTPLocal: true` を明示した場合のみ許可(既定拒否)**。`LLMBENCH_API_TOKEN` 未設定時は loopback bind のみとし、local target は常に拒否
- `GET /v1/runs/{id}` → Run 全体(Phase/HookState/LeaseState/ExecutionState/Artifacts)。**公開 URL は返さない**(恒久公開は controller の責務外。preview URL は `preview.base_url` から導出する)
- bearer 認証: `LLMBENCH_API_TOKEN` 設定時のみ要求
- handler はデコード → `run.Service`(Submit/Status interface をここで宣言)呼び出しのみ

### 4.12 adoption と CD(恒久公開)

**不変条件**:

- `run success ≠ publication`。run の終了条件に公開を含めない
- `preview ≠ public site`。プレビューは認証付きの一時配信、公開サイトは main に存在する採用成果物のみ
- preview・公開サイトの隔離は「**プログラム的通信と資格情報アクセスの遮断**」までを保証する(自己 navigation による HTTP 要求は保証対象外。§4.9 / §4.12)
- review / adopt / publication の同一性は **URL ではなく artifact digest** で結ぶ。digest の定義は §4.4 の `ArtifactDigest(outputDir)` 1 箇所だけとし、**preview・review・adopt・`adopted verify` は必ず同じ関数を使う**(独自の hash 規則を再実装しない)
- `adopt` は**人間が認可した materialization**。コマンドは採否を判断しない
- **公開ゲートは「採用 artifact が `main` 上に存在すること」**。したがって main への direct push を禁止し、**PR 必須の branch protection を運用要件**とする(workflow のトリガーは main push だが、人間の merge 以外で main が動かないことが前提)

フロー(順序厳守): `review 完了(比較相手が存在する場合)→ adopt → output/manifest.json を含む PR → 人間が merge → CI → GitHub Pages`

- `--review` の扱い(**機械的な述語で判定する**。呼び出し側の自己申告に依存しない):
  - `--review` を渡した場合、**`--config <operator.yaml>` を必須**とし、**正である Issue から marker を読んで**検証する(`review status` と同じ経路。ローカル索引だけを信用しない)。検証内容は「marker が記録した **(run_id, artifact_digest) のペア**のうち baseline か candidate が adopt 対象 run と一致し、その digest が実際に adopt する payload の digest と一致すること」。**digest 一致だけでは通さない**(run-B の bytes が偶然 run-A と同じとき、人間がレビューしていない run を adopt できてしまうため)
  - **`--review` を省略できるのは、その model に adopt 済み artifact が 1 件も無い場合のみ**(`experiments/<model-id>/**/output/manifest.json` を走査して 0 件 = 未採用のモデル。この最初の採用が baseline であり、比較相手が原理的に存在しない)。省略時は manifest の `review` を `null` とし、`site build` は「未レビュー」として表示する
  - 上記以外の採用は `--review` 必須。無ければ**書き込み前に失敗**する。**述語は「新しい experiment-id を選べば常に省略できる」形にしてはならない**(destination 単位の判定は review 迂回になる)
  - **「review 完了」の述語**(marker の存在だけでは不十分): Issue 正本に**有効な vote marker が 1 件以上**あり、**最新 vote が adopt 対象 run を選んでいる**こと(`A` → baseline、`B` → candidate)。**`tie`/`invalid` は採用不可**として拒否する(未解決の比較から採用しない)。vote が 1 件も無い review は `request` 済みでも未完了として拒否する
  - **「最新 vote」の順序規則(順序で採用結果が変わるため固定する)**: **GitHub Issue comment の昇順**(API が返す comment ID 順。`bot_login` が作成した有効な vote marker のみを対象)で並べ、**最後の 1 件**を最終決定とする。**marker payload の `at`(時刻)は順序決定に使わない**(payload は表示用の情報であり、正本は Issue の comment 列)。**多数決はしない**。**`review status` と `adopt --review` は必ず同じ順序規則を共有する**(`review` パッケージの 1 関数として実装し、呼び出し側で再実装しない)
  - **`manifest.json` が存在するが schema 検証に失敗する場合は hard error** とし、「manifest が無い」とは絶対に扱わない(fail-open 禁止)`site build` は review の無い採用を「未レビュー」として明示し、review 済みと区別して表示する

**adopt**: `llmbench adopt <run-id> --into experiments/<model-id>/<experiment-id> [--review <review-id> --config <operator.yaml>] [--write]`

- 対象は `<output>/runs/<runID>/output/` の全ファイル(payload)。symlink は拒否。run record の provenance(run ID・benchmark fingerprint・prompt sha256・input_commit・model tree digest・**`ControllerVersion`(submit 時に Run へ永続化した実行時バイナリの version。Fingerprint からは復元できない)**)を manifest に写す
- **レビューした artifact と adopt する artifact が同一であることを digest で検証**する(`--review` 指定時)
- 書き込みは temp dir → copy → hash → manifest → fsync → atomic rename。**atomic に置換するのは `<into>/output/` だけ**であり、`<into>/config.yaml` / `README.md` などには一切触れない(`output/` 以外の削除も行わない)
- 既存 `output/` の扱い:
  - 有効な manifest があり **`run_id` と `artifact_digest` の両方**が一致 → **成功(冪等 no-op)**
  - 有効な manifest があり digest は同一だが `run_id` が異なる → **provenance 更新として許可**する(payload は再検証して同一であることを確認し、manifest を新しい run の来歴で書き換える。CLI は「来歴を更新した」と明示する)
  - 有効な manifest があり digest が異なる → **拒否**(別の採用が既にある)
  - 有効な manifest が無い(未採用の placeholder `index.html` や空ディレクトリ)→ **置換してよい**(dry-run で差分を列挙してから `--write`)
- **非空ディレクトリの置換手順**(`os.Rename` は空でないディレクトリを置換できないため、RemoveAll→Rename ではクラッシュ時に `output/` 消失状態が残る)。**各 rename の後に親ディレクトリを fsync** して durability を確保する:
  1. `<into>/.adopt-tmp-<runID>/` に新 payload + manifest を書き、各ファイルと dir を fsync
  2. 既存 `output/` があれば `<into>/.adopt-bak-<runID>/` へ rename → **親 dir fsync**(backup 名を durable にする)
  3. `.adopt-tmp-*` を `output/` へ rename → **親 dir fsync**(新しい `output/` を durable にする)
  4. `.adopt-bak-*` を削除 → **親 dir fsync**(cleanup も durable)
- **クラッシュ復旧の決定表**(次回の adopt 実行が最初にこの表で状態を判定し、書き込み前に正規化する):

  | 観測された `output/` | `.adopt-tmp-*` | `.adopt-bak-*` | 正とする状態と動作 |
  |---|---|---|---|
  | 有効な manifest あり | なし | なし | 採用済み。`run_id`+digest 一致なら no-op、異なれば拒否 |
  | なし | あり | あり | 中断(step 2〜3 の間)。**tmp 完全 → tmp→output + 親 fsync、bak 削除 + 親 fsync**。**tmp 不完全 → tmp 削除 + 親 fsync、bak→output + 親 fsync、最初からやり直し** |
  | なし | あり | なし | 中断(初回採用の tmp→output 直前)。**tmp 完全 → tmp→output + 親 fsync**。**tmp 不完全 → tmp 削除 + 親 fsync、新規作成からやり直し** |
  | なし | なし | あり | 中断(step 3 の直前)。**bak を output へ戻し + 親 fsync**、最初からやり直し |
  | あり | あり | あり/なし | 中断(step 4 前)。**bak/tmp を破棄 + 親 fsync**し、`output/` を正として採用判定に進む |
  | あり | なし | あり | step 4 の途中。`output/` を正として **bak を破棄 + 親 fsync** |
  | なし | なし | なし | 未採用(初回)。新規作成 |

  - **「tmp が完全」の述語(機械的に判定する)**: `.adopt-tmp-<runID>/` に `manifest.json` が存在し、**schema 検証に成功**し、**inventory が過不足なし**(列挙された全ファイルが存在し sha256 一致、`index.html` 以外の payload ファイルが無い = single-file 契約。§4.3)、**`artifact_digest` が再計算値と一致**する。1 つでも欠ければ「不完全」として破棄する(fail-closed)

  - **`.adopt-*` は一時領域**であり、`adopted verify`/`site build` は `output/manifest.json` だけを見るため公開物には現れない。**採用が成功するまで commit しない**
- 既定は dry-run(`--write` で実体化)
- **`--into` の confinement**(ユーザー入力なので慣例に頼らない): リポジトリ root(`--root`)配下であること、`experiments/<model-id>/<experiment-id>` の 2 段の正確な形であること、`..`・絶対パス・backslash を拒否し、**symlink を経由した root 外への脱出を拒否**する。`<model-id>` は run の `model` と一致しなければならない。これらを満たさない場合は書き込まずに失敗する
- **run と採用先 experiment の結び付き**: `filepath.Dir(run.Experiment) == <into>` を必須とする(run が記録した recipe のディレクトリ以外へ adopt できない)。これにより `<into>/config.yaml` と実際に実行された recipe が一致する。`adopted verify` も manifest の `model`/`experiment_id` が**実際のディレクトリ位置と一致**することを検証する
- `manifest.json`(`schema_version: 1`): `run_id` / `experiment_id` / `model` / `benchmark_fingerprint` / `artifact_digest` / `prompt_sha256` / `input_commit` / `model_tree_digest` / `controller_version` / `adopted_at` / `review{review_id,issue_url}`(比較相手が無い場合は `null`) / `artifacts[{path,sha256,size}]`
- **`output/manifest.json` は予約名**である。benchmark が `output/manifest.json` を生成した場合、**runner は seal 時に失敗させる**(そのまま通すと preview では配信対象なのに digest 対象外となり、adopt の manifest とも衝突する)。payload 内の他階層の `manifest.json` は通常ファイルとして扱ってよい
- **artifacts は payload の完全な inventory** として扱い、**`manifest.json` 自身を含めない**(自己参照で hash 不能になるため)。`ArtifactDigest` も payload のみから計算する(§4.4)。したがって `output/` の内容は「`artifacts` に列挙された全ファイル + `manifest.json`」と**過不足なく一致**しなければならない
- experiment-id に日付プレフィックスを強制しない。時系列は `adopted_at` が持つ(表示順を identity に持ち込まない)

**`llmbench adopted verify --root experiments`**(CI が実行):

- manifest にある → ファイルが存在し sha256 一致 / `output/` にある → manifest にも必ず存在(過不足なし。**例外は `manifest.json` 自身のみ**)
- `artifact_digest` が payload から再計算した `ArtifactDigest` と一致する(§4.4 と同じ関数)
- **global invariant: `review: null`(review 無し採用)は 1 model につき最大 1 件**。並行 PR がそれぞれ「その branch 上では 0 件」と判定して 2 件目を入れるケースは、**main への merge 後に走る CI(`adopted verify`)が違反として失敗**させる(branch protection による最新 main への update 必須化と併用する)
- `index.html` 必須 / symlink 拒否 / `..`・絶対パス拒否 / schema 検証 / **`index.html` 以外の payload ファイルが無いこと(単一ファイル契約。§4.3)**
- **hash・inventory・path 安全性の検証は Go 側(`internal/provenance` を再利用)を正**とし、サイト側ツールチェーンに再実装しない

**`llmbench site build --root experiments --out <dir>`**: `manifest.json` を持つ実験だけを対象に静的サイトを生成する(`output/` があるだけのプレースホルダは公開しない)。index は `adopted_at` 降順 → model 昇順 → experiment-id 昇順。review の無い採用は「未レビュー」として表示する。**ネットワーク不要・read-only**。controller と同じバイナリ・同じ検証を使うため CI と手元で結果が一致する

**公開サイトの隔離**(GitHub Pages はカスタムレスポンスヘッダを返せないため、ヘッダに依存しない構造にする):

- **raw artifact を直接 navigate できる公開リソースにしない**: `site build` は採用 `index.html` を公開パスへ複製せず、**wrapper ページへ `srcdoc` として inline 埋め込み**する(srcdoc でも sandbox なしでは同一 origin になるため、sandbox と必ずセット)。
- **srcdoc の生成は context-aware escaping を必須契約とする**: wrapper は Go の `html/template` 等で生成し、**artifact の bytes は必ず `srcdoc` 属性値として escape する**。**文字列連結で `<iframe srcdoc="` + 生 HTML + `">` を組み立てる実装は禁止**(生成 HTML は完全に untrusted で、`"` などを通じて wrapper 側へ HTML 注入でき、iframe sandbox と CSP を両方迂回する)。meta CSP は**artifact 内の最初の script/resource より前**に挿入し、適用前に何かが実行されないようにする埋め込む bytes は **git 上の adopt 済み artifact から読んだものだけ**で、**preview URL を埋め込み元にしない**(preview は認証付きの一時配信であり、恒久サイトが依存すると PV 削除で壊れ、外部閲覧者も OIDC を通れない)
- **役割の分離**: `iframe sandbox="allow-scripts"` は **origin/capability 隔離**(同一 origin 化の防止)であり、`connect-src` 等の送信先制限は **CSP が担う**。したがって framed document の**先頭に `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline' <許可 CDN 固定>; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'">` を挿入**する(preview の CSP ヘッダと同等の内容。`sandbox` ディレクティブは header 専用なので CD 側では使えない)。`site build` は「wrapper の iframe 属性」と「埋め込み文書の meta CSP」を必ず対で生成し、wrapper 以外から raw artifact へ到達する URL を出力しない

**CI(`.github/workflows/pages.yml`)**: PR では `adopted verify` + `site build` のみ。main push で build artifact を `upload-pages-artifact` → `deploy-pages`(`pages: write` / `id-token: write`)。**ホスト差し替え(Cloudflare Pages 等)は workflow の責務**であり controller は無変更

**現段階の簡易実装(差し替え前提)**: 静的サイトは Go の `site build` が生成する最小構成から始める。SSG を導入する場合は workflow の build ステップだけを差し替える。プレビューの外部公開(Cloudflare Access/Workers 等)と最適化ラウンドの multi-attempt は今回のスコープ外(§10)

**pre-v1.6 record の互換方針**(v1.6 で `ArtifactDigest`/`ControllerVersion` を追加したことに伴う):

- `finalizing` 停留 record は succeeded へ移行する(§3.1)
- それ以外の旧 record は **v1.6 の preview / review / adopt の対象外**とする(`ArtifactDigest` が空のため preview は 404、`ControllerVersion` が空のため adopt は拒否)。**disk 上に artifact が残っていても自動 backfill はしない**(digest は seal の瞬間にしか正当に計算できない)。必要な場合は同一 recipe を再実行して新しい run を作る
- 旧 `succeeded` record は従来どおり読める(表示のみ)。`PublishError`/`PublicURL` は decode 互換のため残すが、新規 run では書かない

**削除対象(v1.5 からの差分、フェーズ 11)**: `internal/pages`、`runner.PublishFinalizer`、`run.Finalizer`/`FinalizeResult`、`PhaseFinalizing` への新規遷移、`PublishError`/`PublicURL` の新規書き込み、operator の `site:` ブロック。この時点で旧 `site:` 設定は黙って無視せず**明示エラーで fail** させる

---

## 5. CLI 仕様

```
llmbench validate <experiment.yaml>
llmbench submit <experiment.yaml> [--commit <full-sha>]     # Engine.Drain で同期駆動
llmbench status <run-id>
llmbench serve --config server.yaml [--state .state] [--output runs]
                [--retry-interval 30s] [--coordination-namespace <ns>] [--lease-name <name>] [--kubeconfig <path>]
                [--preview-addr 127.0.0.1:8081] [--preview-public]   # §4.9(preview 専用リスナ)
llmbench sandbox --namespace <ns> acquire <run-id> <warm-pool>
                | run <claim> '<sh-command>' | pull <claim> <src> <dst> | release <run-id>
llmbench review request <baseline-run-id> <candidate-run-id> --issue <n> --config <operator.yaml>
        | vote <review-id> --choice A|B|tie|invalid [--notes ...] --config <operator.yaml>
        | status <review-id> --config <operator.yaml>
llmbench adopt <run-id> --into experiments/<model-id>/<experiment-id> [--review <review-id> --config <operator.yaml>] [--write]
llmbench adopted verify --root experiments          # CI(§4.12)
# --review は比較相手が存在する採用では必須(無い場合は baseline のみ許可。§4.12)
llmbench site build --root experiments --out _site  # CI(§4.12)
```

環境変数: `LLMBENCH_API_TOKEN`、`LLMBENCH_GITHUB_TOKEN`、`LLMBENCH_DISCORD_WEBHOOK`。`LLMBENCH_GITHUB_TOKEN` は Issue 記録と GitOps PR のみに使う(**site repo への書き込み権限は不要**)。preview は Ingress + クラスタ認証で守る(§4.9)。

## 6. 実装フェーズと完了条件

| # | 内容 | 完了条件 |
|---|------|---------|
| 1 | 骨格縦断スライス | experiment/operator(順序・limits 検証含む)/run(状態機械+Dispatcher+worker+TargetLease・実行 write-ahead)/runner(local, recipe snapshot 使用)/filestore/exec hook + validate/submit/status。`-race` 緑。write-ahead 順序・Acquire 失敗が共通 releasing に合流すること・lease busy 待ち・invoking 割り込み→failure・lease 解放後 terminal の単体テスト |
| 2 | serve | httpapi(local target 既定拒否を含む)+ 並列 dispatch + Recover + provenance 記録 |
| 3 | Sandbox 経路 | sandbox クライアント(Start/Stop 冪等、quote テスト)+ git 検証(snapshot vs commit)+ モデル digest + pin + claim hook + 成果物確定保存 |
| 4 | GitOps hook | §4.6 決定テーブルの全分岐 + 「マージ直後クラッシュ」再開(fake clientset + go-github fake) |
| 5 | pages + review(v1.5。公開部分は v1.6 で CD へ移行) | 冪等公開・`_headers`・fingerprint 検証・marker 投票・discord |
| 6 | kube 永続化 | ConfigMap store(不透明 StoreVersion)・条件付き claim 削除・Lease(喪失時 worker cancel) |
| 7 | 仕上げ | AGENTS.md / README / usage.md 更新、chromedp 除去、全テスト緑 |
| 8 | preview API(§4.9) | `provenance.ArtifactDigest`(§4.4)+ Run への `ArtifactDigest`/`ControllerVersion` 記録、別リスナ + 認可規則 + path confinement/CSP/nosniff/size 定数 + artifact root 限定 + seal 検証。operator に `Preview` 型を追加(**`site:` はフェーズ 11 まで残す**)。review の前提を「succeeded + artifact 確定」へ変更し、preview URL は `preview.base_url` から導出。旧 publication はまだ残す |
| 9 | adopt + verify(§4.12) | `adopt`(digest 照合・atomic・冪等)・`manifest.json`・`adopted verify`。不一致・symlink・path 脱出・過不足のテスト |
| 10 | 公開サイト | `site build`(manifest がある実験のみ・決定性テスト)+ `.github/workflows/pages.yml`(PR は verify/build、main で deploy)+ サンドボックス iframe 表示 |
| 11 | 旧 publication 削除 | CD が動作してから実施。`pages`/`PublishFinalizer`/`Finalizer`/`finalizing` 新規遷移/`public_url` 新規書き込み/operator `site:` を削除し、**旧 `site:` 設定を明示エラーにする**。旧 record の legacy 移行テスト |

各フェーズで `go test -race ./...` と `go vet ./...`。実データで実験プレースホルダを書き換えない。

## 7. 既存 docs からの意図的な変更点

1. **PNG preview 廃止**: chromedp と browser sidecar を使わず、生 HTML を人間が比較する。v1.5 は「公開サイトを直接見る」構成だったが、v1.6 では controller が認証付き preview API(§4.9)を提供し、`review request` の前提を「両 run の succeeded + artifact 存在」に変更した。恒久公開は採用成果物の main マージのみをトリガーとする(§4.12)
2. **visual ブロック廃止**。同一条件は BenchmarkFingerprint で担保
3. **同一モデル digest 要求の限定**(§4.8): cross-model 比較を許すため「無条件の一致要求」から「同モデル時のみ」へ
4. **Finalize / finalizing の廃止**(§3.1): 公開を controller の責務から外したため、`releasing` 完了後は `success → succeeded` / `failure → failed` の二択になった。旧 record の `finalizing` は succeeded へ移行する
5. **local target の位置づけ明確化**(§1.3, §4.11): 隔離境界ではない。HTTP 経由 local 実行は既定拒否
6. `docs/usage.md` / `README.md` / `AGENTS.md` の該当記述はフェーズ 7 で更新

## 8. テスト方針

- table-driven + fake(mock ライブラリ不使用)。`run` Engine は fake Hook/Store/HookSource/LeaseStore/Executor で:
  - write-ahead 順序(acquiring/invoking/releasing 保存→外部呼び出し→完了保存)の検証
  - lease: busy 待ち→解放後の自動前進、**条件付き解放(owner 不一致で削除しない・NotFound=成功)**、取得→保存間クラッシュの再開、解放→terminal の順序
  - **Acquire 失敗後の共通 releasing で acquiring hook も解放されること(not_started は解放しない)**
  - **Release の任意 error で released にせず・TargetLease を解放せず・terminal に進まないこと(ErrPending は待機分類)**
  - **ExecutionState: invoking からの復帰が再実行せず failure になること・completed+running は状態破損扱い**
  - **Execute 失敗 run が failed になり、公開処理を一切行わないこと**
  - **AcquireTargetLease の非 busy error で failure→releasing となること(冪等解放で曖昧成功を回収)**
  - **preview: 認可規則(非 loopback bind の拒否)・path 脱出(`..`/絶対パス/symlink)拒否・CSP/nosniff ヘッダ・size 上限・output 外の非配信・SandboxClient 非依存・`ArtifactDigest` が空(= 未確定)なら 404**
- **`ArtifactDigest` が local/sandbox 双方で同一規則であること(golden digest)。`manifest.json` を payload から除外し、`output/` の過不足検出が manifest 自身を誤検出しないこと**
- **adopt のクラッシュ復旧決定表: `output`/tmp/bak の各組み合わせで正規化が一意に定まること(tmp 完了時は swap 完了、bak のみは復元、output 優先)**
- **preview の seal 検証: 記録 digest と一致しない payload を配信しないこと(409)・`ArtifactDigest` 空で 404**
- **runner が benchmark 生成の `output/manifest.json` を seal 時に拒否すること**
- **seal 順序: 親 dir の fsync 前に `ArtifactDigest` が永続化されないこと/失敗時に digest が空のままであること**
- **single-file 契約: `output/` に `index.html` 以外のファイルがある run が seal に失敗すること(local/sandbox 双方)・回収時の 8 MiB 超過が失敗すること**
- **`site build`: srcdoc の escape(`"`・`<` を含む artifact を注入できないこと)と meta CSP が artifact 先頭に来ること・raw artifact へ直接到達する URL を出力しないこと**
- **adopt: `--into` の confinement(root 外・`..`・symlink 脱出・model 不一致・run の experiment ディレクトリ不一致を拒否)、同一 run/digest の再実行が成功(no-op)・別 digest の既存採用を拒否・manifest の無い placeholder を置換できること(他ファイルは不変)・`--review` 省略述語(model に採用 0 件のみ)・**review 完了述語(vote 0 件・tie・invalid の拒否、A/B が対象 run を選んでいること)**・digest 照合失敗が書き込み前に失敗すること**
- **`adopted verify`: 同一 model に `review: null` が 2 件ある状態を違反として検出すること**
- **pre-v1.6 record(`ControllerVersion`/`ArtifactDigest` 空)が preview(404)・adopt(拒否)の対象外であること**
  - **legacy record: `phase=finalizing` + `ExecutionResult=success` + `LeaseState=released` が succeeded へ移行すること**
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
- 決定論的外部名: Claim `llmbench-<runID>`、プロセス handle `runtime-<runID>`、ブランチ `llmbench/{pause,restore}-<runID>`、成果物 `<output>/runs/<runID>/`(input snapshot はその下 `input/`、配信・adopt 対象は `output/`)、採用先 `experiments/<model-id>/<experiment-id>/output/`
- パッケージコメント必須。パッケージ名は提供物を語る
- ログは logr 構造化フィールド

## 10. 実装時に確認すべき未検証事項・将来拡張

1. agent-sandbox SDK v1.0.4 の sandboxd が argv exec を直接受けられるか、長寿命プロセス primitive を扱えるか。不可なら §4.3 の quote 境界実装+決定論的タグによるバックグラウンド起動。マイルストーン 3 冒頭で確認
2. `git archive` の出力形式と Sandbox 側展開(tar)の整合。マイルストーン 3 でスモーク
3. Ingress(クラスタ認証)+ 別リスナで配信した preview の挙動と、生成 HTML の JS が sandbox 内でどこまで動くか。フェーズ 8 で確認
3b. helper: `os.OpenRoot` による root confinement の Go バージョン要件と、`iframe sandbox` 表示での生成 HTML の動作。フェーズ 8/10 で確認
4. (将来拡張)invoke を managed process(`invoke-<runID>`)+ completion receipt にして、`invoking` 割り込みからの再アタッチを可能にする。v1 は「interrupted = failure」で足りる
5. (将来拡張)最適化ラウンドの multi-attempt: attempt 境界は「**artifact が PV へ immutable snapshot として確定した瞬間**」とし、`<output>/runs/<runID>/attempts/<attemptID>/output/` に置く。review/adopt の identity も `{run_id, attempt_id, artifact_digest}` へ拡張する。preview handler を SandboxClient に依存させないため、attempt 完了ごとに harness へ copy してから次 attempt を開始する。**v1.6 では状態機械を実装せず、現行の単発実行を「唯一の attempt 相当」として扱う**
6. (将来拡張)multi-file artifact: v1.6 は self-contained な単一 `index.html` に固定する(§4.3)。付随 JS/CSS/JSON を認める場合は、preview の CSP と公開サイトの `srcdoc` で相対 URL が解決できるよう **bundle/inline の仕様**(および `script-src`/`img-src` の見直し)を先に定義する
7. (将来拡張)プレビューの外部公開: Cloudflare Access での外部 ID 許可、または一時 deployment。社内 OIDC 限定で足りる間は実装しない

## 11. 変更履歴

- v1.6.0: **公開責務の分離**(ユーザー提案 + 外部レビュー)。不変条件を追加: `run success ≠ publication` / `preview ≠ public site` / review・adopt・publication の同一性は artifact digest / `adopt` は人間が認可した materialization / **`main merge` が唯一の恒久公開ゲート**。① controller の publication(旧 §4.9 pages、`Finalizer`/`FinalizeResult`/`finalizing` 新規遷移/`PublicURL`/`PublishError` の新規書き込み/operator `site:`)を削除し、CI(`adopted verify` + `site build` + GitHub Actions → Pages)へ移す(§4.12)。② `§4.9` を **preview**(別リスナ・上流認証・`output/` 限定・CSP `sandbox allow-scripts`・`os.OpenRoot` による confinement・SandboxClient 非依存)に置換。③ review の前提を「succeeded + artifact 存在」に変更し、preview URL は `preview.base_url` から導出(URL を Run に永続化せず、marker に run ID と artifact digest を残す)。④ `adopt`/`manifest.json`/`adopted verify`/`site build` を追加し、hash・inventory・path 検証は Go 側を正とする。⑤ 旧 record の `finalizing` は succeeded へ移行。⑥ 最適化ラウンドの attempt 境界は §10 に設計のみ記載(現行の単発実行を唯一の attempt 相当とする)。⑦ 公開ゲートは「main 上の存在」+ PR 必須の branch protection。⑧ 2 周目のレビュー指摘の反映: **サイト移行を段階化**(`site:` の削除とエラー化はフェーズ 11。8〜10 は `Site`/`Preview` 併存)、**seal を runner の atomic rename + 配信時 digest 再計算(不一致 409)の 2 段で保証**、**`output/manifest.json` を予約名として seal 時に拒否**、**`--review` 省略述語を「`<into>` に有効な manifest が無い場合のみ」と機械化**、**adopt の atomic rename 対象を `<into>/output/` に限定し placeholder 置換規則を明記**、**pre-v1.6 record は preview/adopt 対象外(backfill しない)**、§1.3/§4.6 の旧「公開サイト書き込み権限」記述を削除。⑪ 5 周目のレビュー指摘の反映: **決定表に `output なし / tmp あり / bak なし` を追加し、`output なし / tmp あり / bak あり` の tmp 不完全時の動作を規定**、**「tmp が完全」の述語を schema/inventory/digest/single-file で機械的に固定**(fail-closed)。**「最新 vote」の順序を GitHub comment 昇順の最後の有効 vote と固定**(payload の `at` は使わない・多数決はしない・`review status` と `adopt --review` が同じ関数を共有)。⑩ 4 周目のレビュー指摘の反映: **「review 完了」の述語を固定**(Issue 正本に有効な vote が 1 件以上あり、最新の解決結果が adopt 対象 run を選ぶ。`tie`/`invalid` は拒否)、**`adopted verify` に「`review: null` は 1 model 最大 1 件」の global invariant を追加**、**artifact を self-contained な単一 `index.html` に固定**(multi-file は preview の CSP と公開サイトの `srcdoc` で表示できないため。bundle/inline は §10)、**adopt の two-phase rename に各段の親 dir fsync とクラッシュ復旧決定表を追加**、**Sandbox 回収の per-file 上限を明記**。⑨ 前段の反映: **artifact 同一性の定義を §4.4 の `ArtifactDigest(outputDir)` 1 箇所に固定**(preview/review/adopt/verify が同じ関数を使う)、**`manifest.json` を payload inventory から除外**して自己参照を排除、**preview は `ArtifactDigest` が非空の確定済み artifact のみ配信**、operator に `Preview` 型を追加(旧 `site:` はエラー)、file size 上限は実装定数(8 MiB)、`--review` の必須/任意を明確化(比較相手が無い baseline のみ任意)、公開サイトは **git 上の adopt 済み bytes だけ**を埋め込み preview URL を参照しない、公開ゲートは「main 上の存在」+ PR 必須の branch protection、`ControllerVersion` を Run に永続化、adopt の `--into` confinement を規定

- v1.5.3: 実装完了に伴う更新 — 実装状況の表を追加。`generation`(temperature/seed 等)を recipe に追加し BenchmarkFingerprint に含める契約、review は Issue を投票履歴の正とし `bot_login` を必須とすること、GitOps は判定と Argo 収束を同一 revision で行うこと、Sandbox claim は Ready/削除完了まで収束待ちすることを明記
- v1.5.2: 実装フィードバックの反映 — `internal/hook`(コマンドフック)を構成に追加、fsync の失敗は握り潰さず snapshot 書き込み自体を失敗させる、公開は静的サイト(専用 origin + CSP)で行いスクリーンショットは持たない
- v1.5.1: OK 判定時に指摘された後続対応事項を設計に先折り込み — ProcessHandle を string に、ExpectedFile の所有を provenance へ、ErrVersionConflict 時の worker 動作(再 LoadRun・外部効果禁止)を明記、BuildHookPlan(t, runID)、Drain(ctx, runID)、旧 rollback 表現の削除
- v1.5: 外部レビュー 5 周目の反映 — **blocker**: ① PlannedHook/GitOpsPlan/SandboxPlan を operator パッケージへ移動し operator.BuildHookPlan の戻り型との依存循環を解消(run → operator のみ)、② Release のエラー意味論を一本化(ErrPending 以外の error も released にせず・TargetLease を解放せず・terminal に進まない。ErrPending は UI 分類。ReleaseTargetLease も同様)、AcquireTargetLease の非 busy error は failure→releasing(冪等解放で回収)。**非 blocker**: SandboxClient の用語統一(EnsureSandboxClaim)、Git.VerifyCommit を期待 SHA256 明示型(expected []ExpectedFile)へ変更、テスト方針の旧 rollback 表現を修正
- v1.4: 外部レビュー 4 周目の反映 — **blocker**: ① Acquire 失敗の rollback を専用経路から廃止し共通 releasing に合流(解放対象 = acquiring/acquired/releasing、not_started は解放しない)、② TargetLease 解放契約に「存在しない場合 = 成功(NotFound = success)」を明記し完全冪等化、③ invoking 復帰の制御フローを明示(continue で再 Execute を構造的に防止)+ completed+running の状態破損扱い。**非 blocker**: GitOps 決定テーブルに drift ケース(自 PR merged なのに manifest が逆方向 = 永続エラー)を追加、BuildHookPlan を順序決定の唯一の箇所として定義、Submit の snapshot 保存順を明示(temp → atomic rename → SaveRun、孤立 dir は GC)、用語を TargetLease / SandboxClaim に分離(ClaimState→LeaseState、ClaimStore→LeaseStore)、Fingerprint に RuntimeSignature を追加し milestone 5 で generation 条件フィールドを計画
- v1.3: 外部レビュー 3 周目の反映 — **blocker**: ExecutionState write-ahead の追加(invoking からの復帰は自動再実行せず failure)、claim 解放側の write-ahead(ClaimState=releasing)+ReleaseClaim の owner 条件付き削除契約、recipe snapshot(RecipeJSON・PromptSHA256・input/ ファイル確定保存、runner は snapshot のみ使用)、Execute 失敗 run は finalizing を経由せず failed+FinalizeResult 型の導入+PublishError フィールド化、StoreVersion を不透明文字列に(resourceVersion を数値化しない)、GitOps→Sandbox 順序の設計不変条件化(operator.Load が強制)、operator 側 max_ready/execution_duration の導入、local runner は隔離境界ではないことの明記+HTTP 経由 local 実行の既定拒否。**非 blocker**: FingerprintInput 構造体+canonical serialization、PlannedHook の型付き・バージョン付き化、HookPlanDigest の毎回検証、Artifacts.Dir の永続ボリューム要件、Lease 喪失時の worker context cancel
- v1.2: target claim の write-ahead 化、rollback 対象に最後の acquiring hook を追加、finalizing の一本化、runtime.start の長寿命プロセス化、Artifacts.Dir 決定論化、HookPlan snapshot、Run.Revision(→v1.3 で StoreVersion に改訂)、CSP 現実的構成
- v1.1: 状態機械の再構成、write-ahead、HookSource、並列 dispatch、Finalize の GPU 解放後移動、BenchmarkFingerprint、Pages 隔離要件、argv 境界、gitops 帰属判定
- v1.0: 初版
