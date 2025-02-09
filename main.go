package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// YAML 用の構造体定義
type BranchGroup struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

type SplitConfig struct {
	Branches []BranchGroup `yaml:"branches"`
}

var (
	sourceBranch   string
	baseBranch     string
	filesPerBranch int
	branchPrefix   string
)

var rootCmd = &cobra.Command{
	Use:   "git-split",
	Short: "BASE と SOURCE の差分ファイルを指定個数ごとに新規ブランチへ反映する",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(os.Stderr, "カレントディレクトリ: %s\n", getCurrentDir())
		fmt.Fprintf(os.Stderr, "カレントディレクトリからリポジトリを開こうとしています...\n")

		// リポジトリをカレントディレクトリからオープン
		repo, err := git.PlainOpen(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "重大なエラー: リポジトリのオープンに失敗\n")
			fmt.Fprintf(os.Stderr, "エラー詳細: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("リポジトリを正常に開きました")

		// 利用可能なブランチの一覧を表示
		refs, _ := repo.References()
		fmt.Println("\n利用可能なブランチ一覧:")
		_ = refs.ForEach(func(ref *plumbing.Reference) error {
			if ref.Name().IsBranch() {
				fmt.Printf("- %s\n", ref.Name().Short())
			}
			return nil
		})

		// BASE ブランチの最新コミットとツリーを取得
		fmt.Printf("BASE ブランチ '%s' の参照を取得しています...\n", baseBranch)
		baseRef, err := repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
		if err != nil {
			// 利用可能なブランチ一覧を表示
			refs, _ := repo.References()
			fmt.Println("\n利用可能なブランチ一覧:")
			_ = refs.ForEach(func(ref *plumbing.Reference) error {
				if ref.Name().IsBranch() {
					fmt.Printf("- %s\n", ref.Name().Short())
				}
				return nil
			})
			log.Fatalf("\nBASE ブランチ '%s' の参照取得に失敗: %v", baseBranch, err)
		}
		fmt.Printf("BASE ブランチ '%s' の参照を正常に取得しました\n", baseBranch)
		baseCommit, err := repo.CommitObject(baseRef.Hash())
		if err != nil {
			log.Fatalf("BASE ブランチのコミット取得に失敗: %v", err)
		}
		fmt.Fprintf(os.Stderr, "BASE コミットハッシュ: %s\n", baseRef.Hash())
		baseTree, err := baseCommit.Tree()
		if err != nil {
			log.Fatalf("BASE ツリーの取得に失敗: %v", err)
		}

		// SOURCE ブランチの最新コミットとツリーを取得
		fmt.Printf("SOURCE ブランチ '%s' の参照を取得しています...\n", sourceBranch)
		sourceRef, err := repo.Reference(plumbing.NewBranchReferenceName(sourceBranch), true)
		if err != nil {
			// 利用可能なブランチ一覧を表示
			refs, _ := repo.References()
			fmt.Println("\n利用可能なブランチ一覧:")
			_ = refs.ForEach(func(ref *plumbing.Reference) error {
				if ref.Name().IsBranch() {
					fmt.Printf("- %s\n", ref.Name().Short())
				}
				return nil
			})
			log.Fatalf("\nSOURCE ブランチ '%s' の参照取得に失敗: %v", sourceBranch, err)
		}
		fmt.Printf("SOURCE ブランチ '%s' の参照を正常に取得しました\n", sourceBranch)
		sourceCommit, err := repo.CommitObject(sourceRef.Hash())
		if err != nil {
			log.Fatalf("SOURCE ブランチのコミット取得に失敗: %v", err)
		}
		fmt.Fprintf(os.Stderr, "SOURCE コミットハッシュ: %s\n", sourceRef.Hash())
		sourceTree, err := sourceCommit.Tree()
		if err != nil {
			log.Fatalf("SOURCE ツリーの取得に失敗: %v", err)
		}

		// BASE と SOURCE のツリー差分（追加・変更ファイルのみ）を取得
		changes, err := baseTree.Diff(sourceTree)
		if err != nil {
			log.Fatalf("差分の取得に失敗: %v", err)
		}

		// 差分ファイル一覧の作成（削除は対象外）
		fileSet := make(map[string]bool)
		var diffFiles []string
		for _, change := range changes {
			action, err := change.Action()
			if err != nil {
				log.Fatalf("Change のアクション取得に失敗: %v", err)
			}
			if action == merkletrie.Delete {
				continue
			}
			var fileName string
			if change.To.Name != "" {
				fileName = change.To.Name
			} else if change.From.Name != "" {
				fileName = change.From.Name
			}
			if fileName != "" && !fileSet[fileName] {
				diffFiles = append(diffFiles, fileName)
				fileSet[fileName] = true
			}
		}

		totalFiles := len(diffFiles)
		fmt.Printf("BASE ブランチ '%s' と SOURCE ブランチ '%s' の差分ファイル数: %d\n", baseBranch, sourceBranch, totalFiles)
		if totalFiles == 0 {
			fmt.Println("差分ファイルがありません。")
			return
		}

		// 指定個数ごとにファイル一覧をグループ分けし、SplitConfig 構造体にまとめる
		numBranches := (totalFiles + filesPerBranch - 1) / filesPerBranch
		fmt.Printf("作成するブランチ数: %d\n", numBranches)

		var cfg SplitConfig
		for i := 0; i < numBranches; i++ {
			start := i * filesPerBranch
			end := start + filesPerBranch
			if end > totalFiles {
				end = totalFiles
			}
			group := BranchGroup{
				Name:  fmt.Sprintf("%s_%d", branchPrefix, i+1),
				Files: diffFiles[start:end],
			}
			cfg.Branches = append(cfg.Branches, group)
		}

		// 1ファイルにまとめた YAML として一時ファイルに出力
		yamlData, err := yaml.Marshal(&cfg)
		if err != nil {
			log.Fatalf("YAML へのマーシャルに失敗: %v", err)
		}

		tmpFile, err := os.CreateTemp("", "split-config-*.yaml")
		if err != nil {
			log.Fatalf("一時ファイルの作成に失敗: %v", err)
		}
		tmpFileName := tmpFile.Name()
		if _, err := tmpFile.Write(yamlData); err != nil {
			log.Fatalf("一時ファイルへの書き込みに失敗: %v", err)
		}
		tmpFile.Close()

		// EDITOR (未設定なら "vi") で一時 YAML ファイルを編集
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		// エディタコマンドとオプションを分割
		editorParts := strings.Fields(editor)
		var editCmd *exec.Cmd
		if len(editorParts) > 1 {
			// コマンドに引数がある場合(例:code -w)
			editCmd = exec.Command(editorParts[0], append(editorParts[1:], tmpFileName)...)
		} else {
			// 単一のコマンドの場合(例:vi)
			editCmd = exec.Command(editor, tmpFileName)
		}
		editCmd.Stdin = os.Stdin
		editCmd.Stdout = os.Stdout
		editCmd.Stderr = os.Stderr
		if err := editCmd.Run(); err != nil {
			log.Fatalf("エディタの起動に失敗: %v", err)
		}

		// 編集後の YAML を読み込み、パース
		editedData, err := os.ReadFile(tmpFileName)
		if err != nil {
			log.Fatalf("編集後の一時ファイルの読み込みに失敗: %v", err)
		}
		// 一時ファイルは不要なので削除
		os.Remove(tmpFileName)

		var editedConfig SplitConfig
		if err := yaml.Unmarshal(editedData, &editedConfig); err != nil {
			log.Fatalf("編集後 YAML のパースに失敗: %v", err)
		}

		// 現在のブランチ名を保存
		headRef, err := repo.Head()
		if err != nil {
			log.Fatalf("HEAD の取得に失敗: %v", err)
		}
		currentBranch := headRef.Name().Short()

		// ワークツリーを取得
		worktree, err := repo.Worktree()
		if err != nil {
			log.Fatalf("ワークツリーの取得に失敗: %v", err)
		}

		// 各グループごとに新規ブランチを作成して SOURCE ブランチの内容で更新
		for _, group := range editedConfig.Branches {
			// 編集後にファイルが空の場合はスキップ
			if len(group.Files) == 0 {
				fmt.Printf("ブランチ '%s' では対象ファイルがないため、スキップします。\n", group.Name)
				continue
			}
			fmt.Printf("==> ブランチ '%s' を作成中（対象ファイル数: %d）\n", group.Name, len(group.Files))

			// BASE ブランチにチェックアウトしてから新規ブランチを作成
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewBranchReferenceName(baseBranch),
			})
			if err != nil {
				log.Fatalf("BASE ブランチへのチェックアウトに失敗: %v", err)
			}
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewBranchReferenceName(group.Name),
				Create: true,
				Hash:   baseCommit.Hash,
			})
			if err != nil {
				log.Fatalf("新規ブランチ '%s' の作成に失敗: %v", group.Name, err)
			}

			// 各ファイルについて、SOURCE ブランチの内容で上書き
			for _, file := range group.Files {
				// SOURCE ブランチ上にファイルが存在するか確認
				_, err := sourceTree.File(file)
				if err != nil {
					fmt.Printf("注意: '%s' は SOURCE ブランチ上に存在しません。\n", file)
					continue
				}
				// 必要なディレクトリ作成
				if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					log.Fatalf("ディレクトリ '%s' の作成に失敗: %v", filepath.Dir(file), err)
				}
				// SOURCE ブランチから対象ファイルをチェックアウト
				checkoutCmd := exec.Command("git", "checkout", sourceBranch, "--", file)
				checkoutCmd.Stdin = os.Stdin
				checkoutCmd.Stdout = os.Stdout
				checkoutCmd.Stderr = os.Stderr
				if err := checkoutCmd.Run(); err != nil {
					log.Fatalf("ファイル '%s' のチェックアウトに失敗: %v", file, err)
				}
				// ステージに追加
				if _, err := worktree.Add(file); err != nil {
					log.Fatalf("ファイル '%s' のステージ追加に失敗: %v", file, err)
				}
				fmt.Printf("更新: %s\n", file)
			}

			// ステージ済み変更があればコミット
			status, err := worktree.Status()
			if err != nil {
				log.Fatalf("ワークツリーの状態取得に失敗: %v", err)
			}
			if status.IsClean() {
				fmt.Printf("ブランチ '%s' では変更がなかったため、コミットをスキップします。\n", group.Name)
			} else {
				commitMsg := fmt.Sprintf("Update diff files: %v", group.Files)
				commitHash, err := worktree.Commit(commitMsg, &git.CommitOptions{
					Author: &object.Signature{
						Name:  "go-git-user",
						Email: "go-git@example.com",
						When:  time.Now(),
					},
				})
				if err != nil {
					log.Fatalf("ブランチ '%s' でのコミットに失敗: %v", group.Name, err)
				}
				fmt.Printf("ブランチ '%s' にコミット完了: %s\n", group.Name, commitHash)
			}
		}

		// 最後に元のブランチに戻る
		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(currentBranch),
		}); err != nil {
			log.Fatalf("元ブランチ '%s' へのチェックアウトに失敗: %v", currentBranch, err)
		}
		fmt.Printf("完了。元ブランチ '%s' に戻りました。\n", currentBranch)
	},
}

func getCurrentDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("カレントディレクトリの取得に失敗: %v", err)
	}
	return dir
}

func setupLogging() (*os.File, *log.Logger) {
	logFile, err := os.OpenFile("git-split.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("ログファイルのオープンに失敗: %v", err)
	}

	// 標準出力とファイルの両方に出力するように設定
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	logger := log.New(multiWriter, "", log.LstdFlags)

	return logFile, logger
}

func checkGitConfig(repo *git.Repository) error {
	// git configの確認
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("git configの取得に失敗: %v", err)
	}

	// ユーザー名とメールアドレスの確認
	if cfg.User.Name == "" || cfg.User.Email == "" {
		return fmt.Errorf("git configにユーザー名またはメールアドレスが設定されていません")
	}

	return nil
}

func writeDebug(msg string) {
	f, _ := os.OpenFile("debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}

func main() {
	writeDebug("プログラムを開始します")
	writeDebug(fmt.Sprintf("カレントディレクトリ: %s", getCurrentDir()))

	rootCmd.Flags().StringVarP(&sourceBranch, "source", "s", "", "差分元のブランチ名 (必須)")
	rootCmd.Flags().StringVarP(&baseBranch, "base", "b", "", "比較対象のベースブランチ名 (必須)")
	rootCmd.Flags().IntVarP(&filesPerBranch, "number", "n", 0, "1ブランチあたりに反映するファイル数 (必須)")
	rootCmd.Flags().StringVarP(&branchPrefix, "prefix", "p", "split", "新規ブランチ名のプレフィックス")
	rootCmd.MarkFlagRequired("source")
	rootCmd.MarkFlagRequired("base")
	rootCmd.MarkFlagRequired("number")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
