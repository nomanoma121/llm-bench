# llm-bench

推論runtimeをモデルごとに測定し、Agentに最適化させるためのリポジトリです。

- `cmd/llmbench`: CLI とコントローラ
  - `benchmark` / `compare`: Sandbox 内で使う測定と比較
  - `controller`: GitHub Issue を poll して GPU で job を実行し、PR を作る
  - `sandbox`: Agent が GPU Sandbox を操作するためのコマンド
  - `site`: `experiments/` から GitHub Pages を生成する
- `internal/`
  - `job`: Issue に書く JobSpec
  - `benchmark`: runtime の起動、計測、結果の書き出し、比較
  - `runtime`: llama.cpp / FreeToken アダプタ
  - `controller`: Issue → pause → Sandbox → 実行 → PR → 後片付け
  - `github`: GitHub App 認証と Issue / PR / ファイル操作
  - `gitops`: manifests リポジトリへの PR による推論 workload の pause / restore
  - `sandbox`: Agent Sandbox の claim と exec
  - `harness`: DSH など ACP 対応 harness とのセッション
  - `site`: Pages の生成
- `experiments/<model>/<job-id>/`: 測定結果（PR で入る）
- `models/`, `runtimes/`, `benchmarks/`: モデル情報、runtime、課題
- `charts/llmbench`: コントローラの Helm chart

使い方は [docs/usage.md](docs/usage.md)。
