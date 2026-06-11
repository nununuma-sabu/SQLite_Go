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
- `BLOB`

```text
db > create table people (id integer, name text, height real, weight real)
Executed.
db > insert 1 Alice 165.2 54.3
Executed.
db > insert 2 Bob 172.4 68.1
Executed.
db > select
+----+-------+--------+--------+
| id | name  | height | weight |
+----+-------+--------+--------+
| 1  | Alice | 165.2  | 54.3   |
+----+-------+--------+--------+
| 2  | Bob   | 172.4  | 68.1   |
+----+-------+--------+--------+
Executed.
db >
```

制約:

- 既にレコードがあるテーブルに対する `create table` は拒否します。
- `create or replace table` は既存レコードを削除してスキーマを置き換えます。
- スキーマ定義はDBファイルのメタデータページへ保存します。
- 1行の最大サイズがleaf pageに収まらないスキーマは拒否します。

```text
db > create or replace table people (id integer primary key, name text)
Executed.
db >
```

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

`BLOB` カラムには `@ファイルパス` 形式でファイルの中身をバイナリとして保存できます。
SELECTではバイナリ本体ではなくサイズを表示します。

```text
db > create table images (id integer primary key, name text, data blob)
Executed.
db > insert 1 usagi @usagi.png
Executed.
db > select id, name, data from images;
+----+-------+-----------------+
| id | name  | data            |
+----+-------+-----------------+
| 1  | usagi | BLOB(8085 bytes) |
+----+-------+-----------------+
Executed.
db >
```

## SELECT

`select` は全レコードを主キー順に、カラム名のヘッダつきで表示します。

```text
db > insert 1 alice alice@example.com
Executed.
db > insert 2 bob bob@example.com
Executed.
db > select
+----+----------+-------------------+
| id | username | email             |
+----+----------+-------------------+
| 1  | alice    | alice@example.com |
+----+----------+-------------------+
| 2  | bob      | bob@example.com   |
+----+----------+-------------------+
Executed.
db >
```

`select * from <table>;` も全カラムを表示します。

```text
db > select * from users;
+----+----------+-------------------+
| id | username | email             |
+----+----------+-------------------+
| 1  | alice    | alice@example.com |
+----+----------+-------------------+
| 2  | bob      | bob@example.com   |
+----+----------+-------------------+
Executed.
db >
```

`select <column>, <column> from <table>;` は指定したカラムだけを表示します。

```text
db > select username, email from users;
+----------+-------------------+
| username | email             |
+----------+-------------------+
| alice    | alice@example.com |
+----------+-------------------+
| bob      | bob@example.com   |
+----------+-------------------+
Executed.
db >
```

数値型のカラムや数値リテラルは、SELECT句で `+`、`-`、`*`、`/` の四則演算に使えます。
`*` と `/` は `+` と `-` より先に評価し、括弧で評価順を指定できます。
ゼロ除算やNULLを含む演算結果は `NULL` として表示します。

```text
db > select username, id + 10, (id + 1) / 2 from users;
+----------+---------+--------------+
| username | id + 10 | (id + 1) / 2 |
+----------+---------+--------------+
| alice    | 11      | 1            |
+----------+---------+--------------+
| bob      | 12      | 1.5          |
+----------+---------+--------------+
Executed.
db >
```

`where` で条件を指定できます。
対応している条件は `=`、`!=`、`<>`、`<`、`<=`、`>`、`>=`、`is null`、`is not null` です。
複数条件は `and` / `or` でつなげます。
`and` は `or` より先に評価します。
括弧で条件式の評価順を指定できます。

```text
db > select username, email from users where id = 2;
+----------+-----------------+
| username | email           |
+----------+-----------------+
| bob      | bob@example.com |
+----------+-----------------+
Executed.
db > select username from users where id >= 2;
+----------+
| username |
+----------+
| bob      |
+----------+
Executed.
db > select username from users where id >= 2 and email is not null;
+----------+
| username |
+----------+
| bob      |
+----------+
Executed.
db > select username from users where id = 1 or id = 2;
+----------+
| username |
+----------+
| alice    |
+----------+
| bob      |
+----------+
Executed.
db > select username from users where (id = 1 or id = 2) and email is not null;
+----------+
| username |
+----------+
| alice    |
+----------+
| bob      |
+----------+
Executed.
db > select id from users where email is not null;
+----+
| id |
+----+
| 1  |
+----+
| 2  |
+----+
Executed.
db >
```

集約関数として `count(*)`、`count(column)`、`sum(column)`、`avg(column)`、`min(column)`、`max(column)` を使えます。
`group by <column>` を指定すると、カラム値ごとに集約します。

```text
db > create table sales (id integer primary key, region text, amount integer)
Executed.
db > insert 1 East 10
Executed.
db > insert 2 East 20
Executed.
db > insert 3 West 7
Executed.
db > select region, count(*), sum(amount), avg(amount) from sales group by region;
+--------+----------+-------------+-------------+
| region | count(*) | sum(amount) | avg(amount) |
+--------+----------+-------------+-------------+
| East   | 2        | 30          | 15          |
+--------+----------+-------------+-------------+
| West   | 1        | 7           | 7           |
+--------+----------+-------------+-------------+
Executed.
db >
```

`order by` で結果を並び替えできます。
方向は `asc` / `desc` を指定でき、省略時は `asc` です。

```text
db > select username, email from users order by username desc;
+----------+-------------------+
| username | email             |
+----------+-------------------+
| bob      | bob@example.com   |
+----------+-------------------+
| alice    | alice@example.com |
+----------+-------------------+
Executed.
db > select username from users where id >= 1 order by id asc;
+----------+
| username |
+----------+
| alice    |
+----------+
| bob      |
+----------+
Executed.
db >
```

`limit` で表示する件数を制限できます。
`order by` と組み合わせた場合は、並び替え後の先頭から指定件数を表示します。

```text
db > select username from users order by id desc limit 1;
+----------+
| username |
+----------+
| bob      |
+----------+
Executed.
db >
```

`select <id>` は指定したIDのレコードだけを表示します。

```text
db > select 2
+----+----------+-----------------+
| id | username | email           |
+----+----------+-----------------+
| 2  | bob      | bob@example.com |
+----+----------+-----------------+
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
+----+-------+--------+--------+
| id | name  | height | weight |
+----+-------+--------+--------+
| 1  | Alice | 165.2  | 54.3   |
+----+-------+--------+--------+
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
