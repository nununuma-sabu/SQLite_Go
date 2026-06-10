# 実装メモ

## B-Tree

- leaf node内では主キーをキーとして二分探索し、キー順になる位置へ挿入します。
- root leaf node、子leaf node、internal nodeの分割に対応しています。
- internal nodeは再帰的に検索できます。
- leaf nodeには右隣leaf nodeへの `next_leaf` を持たせ、`select` で複数leaf nodeをまたいで全件走査します。
- leaf node split後の親internal node更新も実装しています。
- leaf nodeセルは可変長で、セルポインタ配列とページ末尾側のペイロード領域を使います。

## 型システム

SQLite風の型アフィニティを `schema.go` に実装しています。

| 型名の判定 | affinity |
| --- | --- |
| `INT` を含む | `INTEGER` |
| `CHAR` / `CLOB` / `TEXT` を含む | `TEXT` |
| `BLOB` または型指定なし | `BLOB` |
| `REAL` / `FLOA` / `DOUB` を含む | `REAL` |
| それ以外 | `NUMERIC` |

BooleanやDate/Time専用型は持たず、SQLite同様に `NUMERIC` affinityとして扱います。

## Row形式

Rowはスキーマのカラム順に、値のStorageClassと実データを可変長で保存します。
現行形式は `SGR2` magicを持ち、古い `SGR1` 形式と固定長Row形式も読み戻せます。

## enum相当の表現

GoにはC言語の `enum` と同じ構文はありません。
このプロジェクトでは `ExitCode`、`PrepareResult`、`ExecuteResult` などを独自型と `const` / `iota` で表現しています。

## ファイル構成

| ファイル | 役割 |
| --- | --- |
| `main.go` | エントリーポイント |
| `repl.go` | プロンプト表示と入力ループ |
| `meta.go` | メタコマンド |
| `statement.go` | `create table` / `insert` / `select` のパースと実行 |
| `schema.go` | 型システムとスキーマ定義 |
| `row.go` | Rowのシリアライズと表示 |
| `database_metadata.go` | DBファイルに保存するスキーマメタデータ |
| `pager.go` | DBファイルとページキャッシュ |
| `node.go` | B-Treeノードのレイアウトと表示 |
| `cursor.go` | B-Tree上の探索位置と走査 |
| `btree.go` | leaf/internal nodeの挿入と分割 |
| `constants.go` | 定数とenum相当の型 |
| `types.go` | 主要な構造体 |

## 処理の流れ

最終的に目指す処理の流れは以下です。

![SQLite-like processing flow](sqlite_like_processing_flow.png)
