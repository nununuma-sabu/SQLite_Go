# SQLite Go

SQLiteのような小さなデータベースをGoで段階的に実装する学習用プロジェクトです。

現在の実装では、対話形式のプロンプトを表示し、`.exit` が入力されたら終了します。
`create table` で実行中のテーブルスキーマを定義し、`insert` でレコードを保存し、`select` で保存済みの全レコードを表示します。
データは指定したDBファイルへ保存されるため、プログラムを終了しても残ります。
保存形式はB-Treeのleaf nodeを使う形に進んでいます。
root leaf node、子leaf node、internal nodeの分割に対応しています。
leaf node内ではIDをキーとして二分探索し、キー順になる位置へ挿入します。
internal nodeは再帰的に検索できます。
leaf nodeには右隣leaf nodeへの `next_leaf` を持たせているため、`select` では複数leaf nodeをまたいで全件走査できます。
leaf node split後の親internal node更新も実装しています。

終了コードは `ExitCode` という独自型で表現しています。
GoにはC言語の `enum` と同じ構文はありませんが、`const` と `iota` を使うことでenumに近い定数定義ができます。

## 必要なもの

- Go

インストール確認:

```bash
go version
```

例:

```text
go version go1.26.2 linux/amd64
```

## 実行方法

プロジェクトのルートディレクトリで以下を実行します。

```bash
go run . test.db
```

`test.db` は保存先のDBファイル名です。存在しない場合は自動で作成されます。
ファイル名を指定しない場合は起動できません。

起動すると、以下のようにプロンプトが表示されます。

```text
db >
```

`.exit` を入力すると終了します。

```text
db > .exit
```

`.exit` で終了すると、変更内容がDBファイルへ書き戻されます。

## メタコマンド

`.exit` はDBを閉じて終了します。

```text
db > .exit
```

`.constants` は、現在のページ・ノード・セルのレイアウト定数を表示します。

```text
db > .constants
Constants:
ROW_SIZE: 293
COMMON_NODE_HEADER_SIZE: 6
LEAF_NODE_HEADER_SIZE: 14
LEAF_NODE_CELL_SIZE: 297
LEAF_NODE_SPACE_FOR_CELLS: 4082
LEAF_NODE_MAX_CELLS: 13
db >
```

`.btree` は、現在のB-Tree構造を表示します。

```text
db > insert 3 user3 person3@example.com
Executed.
db > insert 1 user1 person1@example.com
Executed.
db > insert 2 user2 person2@example.com
Executed.
db > .btree
Tree:
- leaf (size 3)
  - 1
  - 2
  - 3
db >
```

## CREATE TABLE

`create table` は、空のテーブルに対してカラム定義を設定します。
現時点では `id` カラムを `INTEGER` として含める必要があり、この `id` をB-Treeのキーとして使います。

対応している型:

- `INTEGER`
- `TEXT`
- `REAL`

```text
db > create table people (id integer, name text, height real, weight real)
Executed.
db > insert 1 Alice 165.2 54.3
Executed.
db > insert 2 Bob 172.4 68.1
Executed.
db > select
(1, Alice, 165.2, 54.3)
(2, Bob, 172.4, 68.1)
Executed.
db >
```

注意:

- 既にレコードがあるテーブルに対する `create table` は拒否します。
- スキーマ定義はまだDBファイルへ保存していません。再起動後はデフォルトスキーマに戻ります。
- 1行のサイズが現在のセル値領域である `ROW_SIZE` を超えるスキーマは拒否します。

`insert` で始まる入力は、insertステートメントとして仮実行されます。
形式は `insert <id> <username> <email>` です。

制約:

- `id` は0以上の整数
- `username` は32文字以内
- `email` は255文字以内

```text
db > insert 1 user user@example.com
Executed.
db >
```

`select` は、保存済みの全レコードを表示します。
root split後も、leaf nodeの `next_leaf` を辿って全件表示します。

```text
db > insert 1 alice alice@example.com
Executed.
db > insert 2 bob bob@example.com
Executed.
db > select
(1, alice, alice@example.com)
(2, bob, bob@example.com)
Executed.
db >
```

`select <id>` は、指定したIDのレコードだけを表示します。

```text
db > insert 1 alice alice@example.com
Executed.
db > insert 2 bob bob@example.com
Executed.
db > select 2
(2, bob, bob@example.com)
Executed.
db >
```

root leaf nodeが満杯になると、左右のleaf nodeへ分割し、新しいroot internal nodeを作ります。

```text
db > .btree
Tree:
- internal (size 1)
  - leaf (size 7)
    - 1
    - 2
    - 3
    - 4
    - 5
    - 6
    - 7
  - key 7
  - leaf (size 7)
    - 8
    - 9
    - 10
    - 11
    - 12
    - 13
    - 14
db >
```

root split後もinternal nodeを辿ってinsertできます。
また、子leaf nodeのsplit後は親internal nodeのkeyも更新します。
internal nodeが満杯になった場合はinternal node splitを行い、必要なら新しいrootを作ります。

```text
db > .btree
Tree:
- internal (size 1)
  - internal (size 2)
    - leaf (size 7)
      - 1
      - 2
      - 4
      - 5
      - 6
      - 7
      - 8
    - key 8
    - leaf (size 11)
      - 9
      - 10
      - 12
      - 13
      - 14
      - 15
      - 18
      - 19
      - 20
      - 21
      - 22
    - key 22
    - leaf (size 8)
      - 24
      - 25
      - 29
      - 30
      - 31
      - 32
      - 33
      - 35
  - key 35
  - internal (size 3)
    - leaf (size 12)
      - 36
      - 37
      - 39
      - 40
      - 43
      - 44
      - 46
      - 47
      - 48
      - 49
      - 50
      - 51
db >
```

同じDBファイルを指定して再起動すると、前回保存したレコードを読み込めます。

```text
$ go run . test.db
db > insert 1 user1 person1@example.com
Executed.
db > .exit

$ go run . test.db
db > select
(1, user1, person1@example.com)
Executed.
db > .exit
```

`insert` に必要な値が足りない場合は、構文エラーとして表示されます。

```text
db > insert 1 alice
Syntax error. Could not parse statement.
db >
```

`id` が負数の場合は、エラーとして表示されます。

```text
db > insert -1 cstack foo@bar.com
ID must be positive.
db >
```

`username` または `email` が長すぎる場合は、エラーとして表示されます。

```text
db > insert 1 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa person@example.com
String is too long.
db >
```

同じ `id` のレコードを追加しようとすると、重複キーとしてエラーになります。

```text
db > insert 1 user1 person1@example.com
Executed.
db > insert 1 user1 person1@example.com
Error: Duplicate key.
db >
```

`.` で始まる未知の入力は、未対応のメタコマンドとして表示されます。

```text
db > .unknown
Unrecognized command '.unknown'.
db >
```

`.` で始まらない未知の入力は、未対応のステートメントとして表示されます。

```text
db > hello
Unrecognized keyword at start of 'hello'.
db >
```

## テスト方法

```bash
go test ./...
```

この環境のようにGoのビルドキャッシュへ書き込めない場合は、`GOCACHE` を `/tmp` 配下に向けて実行します。

```bash
GOCACHE=/tmp/go-build go test ./...
```

## ファイル構成

- `main.go`: エントリーポイント
- `repl.go`: プロンプト表示と入力ループ
- `statement.go`: `insert` / `select` のパースと実行
- `schema.go`: 任意カラム対応に向けた型システムとスキーマ定義
- `row.go`: 固定スキーマRowのシリアライズと表示
- `pager.go`: DBファイルとページキャッシュ
- `node.go`: B-Treeノードのレイアウトと表示
- `cursor.go`: B-Tree上の探索位置と走査
- `btree.go`: leaf/internal nodeの挿入と分割
- `constants.go`: 定数とenum相当の型
- `types.go`: 主要な構造体

## 今後の拡張案

- 削除処理: `delete <id>` を追加し、B-Treeからレコードを取り除けるようにする。
- 更新処理: `update <id> <username> <email>` を追加し、既存レコードを書き換える。
- 条件付きselect: `select where id = ...` のようなSQL風の条件式を扱う。
- エラー整理: `panic` で止めている内部エラーを、戻り値や独自エラー型で扱う。
- DBファイル互換性: ページレイアウトのバージョン情報やマジックヘッダを追加する。
- ページ再利用: 削除済みページや空きページを管理するfree listを実装する。
- 可変スキーマ: 固定Rowではなく、簡単な `CREATE TABLE` と型定義を扱う。
- トランザクション: まずは単純なrollback journalを作り、途中失敗から復旧できるようにする。
- テスト分割: 実装ファイルに合わせて、B-Tree、Pager、REPLなどのテストファイルも分ける。
- コマンドライン改善: `.help`、`.schema`、`.pages` などのデバッグ用メタコマンドを追加する。
- ベンチマーク: 大量insert/selectのベンチマークを追加し、B-Treeの挙動を観察する。

## 型システムのメモ

任意カラム対応に向けて、SQLite風の型システムを `schema.go` に用意しています。
現在の固定Rowテーブルにも、以下のデフォルトスキーマを適用しています。

| カラム | 宣言型 | affinity | 制約 |
| --- | --- | --- | --- |
| `id` | `INTEGER` | `INTEGER` | primary key、0以上 |
| `username` | `TEXT` | `TEXT` | 32文字以内 |
| `email` | `TEXT` | `TEXT` | 255文字以内 |

`insert` と `select <id>` のID検証、`username` / `email` の長さ検証は、このスキーマを参照します。
保存形式はまだ固定長Rowのままですが、Rowのカラムオフセットとサイズはスキーマから計算する `RowLayout` を使います。
将来の `CREATE TABLE` 実装で任意カラムへ広げるための接続点になります。

値そのものの保存形式として、以下のストレージクラスを定義しています。

- `NULL`
- `INTEGER`
- `REAL`
- `TEXT`
- `BLOB`

カラム宣言からは、SQLiteに近い順序で型アフィニティを推定します。

- `INT` を含む型名: `INTEGER`
- `CHAR` / `CLOB` / `TEXT` を含む型名: `TEXT`
- `BLOB` または型指定なし: `BLOB`
- `REAL` / `FLOA` / `DOUB` を含む型名: `REAL`
- それ以外: `NUMERIC`

BooleanやDate/Time専用型は持たず、SQLite同様に `NUMERIC` affinityとして扱います。

## ビルドして実行する方法

実行ファイルを作る場合は以下を実行します。

```bash
go build -buildvcs=false -o sqlite-go .
```

作成した実行ファイルを起動します。

```bash
./sqlite-go test.db
```

## 処理の流れ

最終的に目指す処理の流れは以下です。

![SQLite-like processing flow](docs/sqlite_like_processing_flow.png)
