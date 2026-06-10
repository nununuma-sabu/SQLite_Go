# 使い方

## SQLファイルの実行

DBファイル名の後ろにSQLファイルを指定すると、ファイル内のSQLをセミコロン区切りで実行します。
このモードではREPLの `db >` プロンプトは表示しません。

```bash
go run . test.db setup.sql
```

例:

```sql
-- setup.sql
create table people (
  id integer primary key,
  name text,
  height real
);

insert 1 Alice 165.2;
insert 2 Bob 172.4;
select name, height from people;
```

## CREATE TABLE

空のテーブルに対してカラム定義を設定します。
`INTEGER PRIMARY KEY` カラムをB-Treeのキーとして使います。互換性のため、明示的な主キーがない `id INTEGER` は暗黙の主キーとして扱います。

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

制約:

- 既にレコードがあるテーブルに対する `create table` は拒否します。
- スキーマ定義はDBファイルのメタデータページへ保存します。
- 1行の最大サイズがleaf pageに収まらないスキーマは拒否します。

## INSERT

`insert` は、現在のスキーマのカラム順に値を受け取ります。

デフォルトスキーマ:

```text
create table users (id INTEGER, username TEXT, email TEXT)
```

```text
db > insert 1 alice alice@example.com
Executed.
db >
```

主な制約:

- 主キーは0以上の整数
- デフォルトスキーマの `username` は32文字以内
- デフォルトスキーマの `email` は255文字以内
- 同じ主キーは重複キーとして拒否

## SELECT

`select` は全レコードを主キー順に表示します。

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

`select * from <table>;` も全カラムを表示します。

```text
db > select * from users;
(1, alice, alice@example.com)
(2, bob, bob@example.com)
Executed.
db >
```

`select <column>, <column> from <table>;` は指定したカラムだけを表示します。

```text
db > select username, email from users;
(alice, alice@example.com)
(bob, bob@example.com)
Executed.
db >
```

`where` で条件を指定できます。
対応している条件は `=`、`!=`、`<>`、`<`、`<=`、`>`、`>=`、`is null`、`is not null` です。
複数条件は `and` でつなげます。

```text
db > select username, email from users where id = 2;
(bob, bob@example.com)
Executed.
db > select username from users where id >= 2;
(bob)
Executed.
db > select username from users where id >= 2 and email is not null;
(bob)
Executed.
db > select id from users where email is not null;
(1)
(2)
Executed.
db >
```

`select <id>` は指定したIDのレコードだけを表示します。

```text
db > select 2
(2, bob, bob@example.com)
Executed.
db >
```

## メタコマンド

| コマンド | 説明 |
| --- | --- |
| `.exit` | DBを閉じて終了する |
| `.schema` | 現在のテーブルスキーマを `create table` 形式で表示する |
| `.constants` | ページ・ノード・セルのレイアウト定数を表示する |
| `.btree` | 現在のB-Tree構造を表示する |

例:

```text
db > .schema
create table users (id INTEGER, username TEXT, email TEXT)
db > .btree
Tree:
- leaf (size 2)
  - 1
  - 2
db >
```

## 永続化

同じDBファイルを指定して再起動すると、前回保存したスキーマとレコードを読み込めます。

```text
$ go run . test.db
db > create table people (id integer, name text, height real, weight real)
Executed.
db > insert 1 Alice 165.2 54.3
Executed.
db > .exit

$ go run . test.db
db > .schema
create table people (id integer, name text, height real, weight real)
db > select
(1, Alice, 165.2, 54.3)
Executed.
db > .exit
```

新しいDBファイルでは、page 0をメタデータページ、page 1をB-Tree rootとして使います。
古いDBファイルはpage 0をB-Tree rootとして扱う形式も読み込めるようにしています。

## エラー例

```text
db > insert 1 alice
Syntax error. Could not parse statement.
db > insert -1 cstack foo@bar.com
Primary key must be positive.
db > insert 1 user1 person1@example.com
Executed.
db > insert 1 user1 person1@example.com
Error: Duplicate key.
db > .unknown
Unrecognized command '.unknown'.
db > hello
Unrecognized keyword at start of 'hello'.
db >
```
