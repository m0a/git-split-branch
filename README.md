# git-split-branch

A Git utility tool to split differences between two branches into multiple branches (Go CLI)

[日本語版は下部に続きます](#git-split-branch-日本語版)

## Overview
This tool helps manage large code changes by splitting diffs between a source branch and base branch into multiple smaller branches. Useful for breaking down big PRs/MRs into manageable chunks.


## Installation
```bash
# Install globally
go install github.com/m0a/git-split-branch@latest

# Or clone and build locally
git clone https://github.com/m0a/git-split-branch.git
cd git-split-branch
go build -o git-split-branch main.go
```

## Usage
```bash
git split-branch \
  --source feature-branch \
  --base main \
  --number 10 \
  --prefix split
```

**Options**:
- `--source/-s`: Source branch name (required)
- `--base/-b`: Base branch name (default: main)
- `--number/-n`: Number of files per branch (required)
- `--prefix/-p`: Branch name prefix (default: split)


When you run the command, the tool generates a YAML file (`split-config-*.yaml`) that proposes the files to be split and the branch names:
```yaml
# This YAML file contains the configuration for splitting branches.
# Each branch group specifies a branch name and the list of files to be included in that branch.

branches:
- name: split_1
  files:
  - test/file1.txt
  - test/file2.txt
  
- name: split_2
  files:
  - test/file3.txt

```

After saving, the specified branches will be created.


## License
MIT

---

# git-split-branch (日本語版)

Gitブランチ間の差分を複数のブランチに分割するユーティリティツール(Go CLI製)

## 概要
このツールは、ソースブランチとベースブランチ間の差分を検出し、変更ファイルを複数の小さなブランチに分割します。大規模なPR/MRを管理しやすいサイズに分割する際に有用です。


## インストール方法
```bash
# グローバルインストール
go install github.com/m0a/git-split-branch@latest

# またはローカルでビルド
git clone https://github.com/m0a/git-split-branch.git
cd git-split-branch
go build -o git-split-branch main.go
```

## 使い方
```bash
  git split-branch \
  --source feature-branch \
  --base main \
  --number 10 \
  --prefix split
```

**オプション**:
- `--source/-s`: ソースブランチ名(必須)
- `--base/-b`: ベースブランチ名(デフォルト: main)
- `--number/-n`: 1ブランチあたりのファイル数(必須)
- `--prefix/-p`: ブランチ名プレフィックス(デフォルト: split)


コマンドを実行すると、ツールはファイルの分割とブランチ名を提案するYAMLファイル(`split-config-*.yaml`)を生成します:

```yaml
# This YAML file contains the configuration for splitting branches.
# Each branch group specifies a branch name and the list of files to be included in that branch.

branches:
- name: split_1
  files:
  - test/file1.txt
  - test/file2.txt
  
- name: split_2
  files:
  - test/file3.txt

```

保存後に対象のブランチが実際に作成されます。


## ライセンス
MITtest
