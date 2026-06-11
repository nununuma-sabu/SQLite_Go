# ロードマップ

## 段階的な拡張メモ

固定長Rowと単一主キーB-Treeの前提を外していくため、以下の順で小さく進めます。

1. 完了: `id` 固定をやめて、`PrimaryKeyColumn()` を常に使う。
2. 完了: Rowの値取得・キー取得をスキーマベースに整理する。
3. 完了: Rowシリアライズを可変長化する。
4. 完了: leaf nodeセルを可変長化する。
5. 着手: NULL、NOT NULL、複合主キー、DEFAULT、CHECK、インデックスへ進む。
   - 完了: NULL値の保存とNOT NULL制約の検査。

## 今後の拡張案

- SELECT拡張:
  - 完了: `where` の比較演算子、`and` / `or`、括弧条件を扱う。
  - 完了: `order by <column> asc|desc` を扱う。
  - 完了: `limit <count>` を扱う。
  - 完了: SELECT句で数値型の四則演算を扱う。
  - 完了: 集約関数 `count(*)`、`count(column)`、`min`、`max`、`sum`、`avg` を扱う。
  - 完了: `group by <column>` と集約関数を組み合わせる。
  - HAVING: GROUP BY後の集約結果を条件で絞り込む。
  - DISTINCT: 重複行を除外する。
  - 式・別名: `select name as display_name` のような表示名を扱う。
  - 複数カラムORDER BY: `order by height desc, name asc` を扱う。
  - OFFSET: `limit` と組み合わせてページングできるようにする。
- 更新処理: `update <id> ...` を追加し、既存レコードを書き換える。
- 削除処理: `delete <id>` を追加し、B-Treeからレコードを取り除けるようにする。
- スキーマ変更: `ALTER TABLE` や既存データを持つテーブルのスキーマ変更を扱う。
- 複合主キー: 複数カラムをキーとして比較・保存できるようにする。
- DEFAULT/CHECK: カラム制約とテーブル制約の実行時検査を増やす。
- インデックス: `UNIQUE` や検索用のセカンダリB-Treeを実装する。
- エラー整理: `panic` で止めている内部エラーを、戻り値や独自エラー型で扱う。
- DBファイル互換性: ページレイアウトのバージョン管理と古いDBファイルの移行処理を整理する。
- ページ再利用: 削除済みページや空きページを管理するfree listを実装する。
- トランザクション: まずは単純なrollback journalを作り、途中失敗から復旧できるようにする。
- テスト分割: 実装ファイルに合わせて、B-Tree、Pager、REPLなどのテストファイルも分ける。
- コマンドライン改善: `.help`、`.pages` などのデバッグ用メタコマンドを追加する。
- ベンチマーク: 大量insert/selectのベンチマークを追加し、B-Treeの挙動を観察する。
