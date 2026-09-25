# llm-bench

Agentが推論runtimeをモデルごとに試し、固定した課題の出力を人間が比較するためのリポジトリです。現在の課題は、LLMにHTMLで3Dモデルを作らせるvisual benchmarkです。

- `models/models.yaml`: モデルの取得情報。モデル本体は`models/<model-id>/`に置き、Git管理しません。
- `runtimes/<engine>/<variant>/`: runtime本体とモデル向けの変更。
- `benchmarks/visual/prompt.md`: 複数の実験で共有する課題。
- `experiments/<model-id>/<experiment-id>/`: 1候補の`config.yaml`、実験ノート、採用した生成物。
- `cmd/llmbench/`: Go製controller・CLIのエントリポイント。共通の実装は`internal/`。
- `charts/llmbench/`: Agent Sandboxとharness用の実験的Helm Chart。現在は単一replicaで、明示的なHTTPリクエストからSandboxで単発benchmarkを実行できます。GitOpsの停止・復帰PRとArgo CD／実Podの状態確認、モデル重みの実行前後の同一性検査、IssueへのA/B比較・投票を実装しています。生成HTMLは認証情報を持たないブラウザsidecarで描画しますが、同一Podのネットワークを共有するため敵対的なHTMLに対する完全な分離ではありません。実クラスタでの統合検証とAgentの最適化ループの接続は未完了です。

合意済みの運用と実装順は[docs/design.md](docs/design.md)と[docs/plan.md](docs/plan.md)に記録しています。実行方法は[docs/usage.md](docs/usage.md)を参照してください。
