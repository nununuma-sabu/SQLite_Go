# SQLite Go

SQLiteのような小さなデータベースをGoで段階的に実装する学習用プロジェクトです。

現在は、1ファイルのDBに対して以下を扱えます。

- 対話形式のREPL
- `CREATE TABLE` による単一テーブルのスキーマ定義
- `insert` による1行追加
- `select` / `select * from ...` / `select column1, column2 from ...` による取得
- 主キーB-Treeへのキー順保存、leaf/internal nodeの分割
- DBファイルへのレコードとスキーマの永続化

## 必要なもの

- Go

```bash
go version
```

## 実行方法

プロジェクトのルートディレクトリでDBファイル名を指定して起動します。

```bash
go run . test.db
```

起動すると `db >` プロンプトが表示されます。終了する場合は `.exit` を入力します。

```text
db > .exit
```

実行ファイルを作る場合は以下を使います。

```bash
go build -buildvcs=false -o sqlite-go .
./sqlite-go test.db
```

## クイック例

```text
db > create table people (id integer primary key, name text, height real, weight real)
Executed.
db > insert 1 Alice 165.2 54.3
Executed.
db > insert 2 Bob 172.4 68.1
Executed.
db > select name, height from people;
(Alice, 165.2)
(Bob, 172.4)
Executed.
db > .exit
```

## ドキュメント

- [使い方](docs/usage.md)
- [実装メモ](docs/implementation.md)
- [ロードマップ](docs/roadmap.md)

## テスト

```bash
go test ./...
```

Goのビルドキャッシュへ書き込めない環境では、`GOCACHE` を `/tmp` 配下に向けます。

```bash
GOCACHE=/tmp/go-build go test ./...
```
