package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// Struct definitions for YAML configuration
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
	Use:   "git-split-branch",
	Short: "Split diff files between two branches into multiple branches",
	Run:   run,
}

func main() {
	rootCmd.Flags().StringVarP(&sourceBranch, "source", "s", "", "Name of the source branch for diff (required)")
	rootCmd.Flags().StringVarP(&baseBranch, "base", "b", "main", "Name of the base branch for comparison")
	rootCmd.Flags().IntVarP(&filesPerBranch, "number", "n", 0, "Number of files per branch (required)")
	rootCmd.Flags().StringVarP(&branchPrefix, "prefix", "p", "split", "Prefix for new branch names")
	rootCmd.MarkFlagRequired("source")
	rootCmd.MarkFlagRequired("number")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) {
	repo, err := openRepository()
	if err != nil {
		log.Fatalf("Critical Error: %v", err)
	}
	displayBranches(repo)

	baseCommit, baseTree, err := getBranchCommitAndTree(repo, baseBranch)
	if err != nil {
		log.Fatalf("Failed to get base branch details: %v", err)
	}

	_, sourceTree, err := getBranchCommitAndTree(repo, sourceBranch)
	if err != nil {
		log.Fatalf("Failed to get source branch details: %v", err)
	}

	diffFiles, err := getDiffFiles(baseTree, sourceTree)
	if err != nil {
		log.Fatalf("Failed to get diff files: %v", err)
	}

	if len(diffFiles) == 0 {
		fmt.Println("No diff files found.")
		return
	}

	cfg := createSplitConfig(diffFiles)
	tmpFileName, err := createTempYAMLFile(cfg)
	if err != nil {
		log.Fatalf("Failed to create temporary YAML file: %v", err)
	}

	if err := editYAMLFile(tmpFileName); err != nil {
		log.Fatalf("Failed to edit YAML file: %v", err)
	}

	editedConfig, err := readEditedYAMLFile(tmpFileName)
	if err != nil {
		log.Fatalf("Failed to read edited YAML file: %v", err)
	}

	if err := createBranches(repo, baseCommit, sourceTree, editedConfig); err != nil {
		log.Fatalf("Failed to create branches: %v", err)
	}
}

func openRepository() (*git.Repository, error) {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %v", err)
	}
	fmt.Println("Repository opened successfully")
	return repo, nil
}

func displayBranches(repo *git.Repository) {
	refs, _ := repo.References()
	fmt.Println("\nAvailable branches:")
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() {
			fmt.Printf("- %s\n", ref.Name().Short())
		}
		return nil
	})
}

func getBranchCommitAndTree(repo *git.Repository, branchName string) (*object.Commit, *object.Tree, error) {
	fmt.Printf("Getting reference for branch '%s'...\n", branchName)
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branchName), true)
	if err != nil {
		displayBranches(repo)
		return nil, nil, fmt.Errorf("failed to get reference for branch '%s': %v", branchName, err)
	}
	fmt.Printf("Successfully got reference for branch '%s'\n", branchName)
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get commit for branch '%s': %v", branchName, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get tree for branch '%s': %v", branchName, err)
	}
	return commit, tree, nil
}

func getDiffFiles(baseTree, sourceTree *object.Tree) ([]string, error) {
	changes, err := baseTree.Diff(sourceTree)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %v", err)
	}

	fileSet := make(map[string]bool)
	var diffFiles []string
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, fmt.Errorf("failed to get action for change: %v", err)
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

	fmt.Printf("Diff files count: %d\n", len(diffFiles))
	return diffFiles, nil
}

func createSplitConfig(diffFiles []string) SplitConfig {
	totalFiles := len(diffFiles)
	numBranches := (totalFiles + filesPerBranch - 1) / filesPerBranch
	fmt.Printf("Number of branches to be created: %d\n", numBranches)

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
	return cfg
}

func createTempYAMLFile(cfg SplitConfig) (string, error) {
	description := "# This YAML file contains the configuration for splitting branches.\n" +
		"# Each branch group specifies a branch name and the list of files to be included in that branch.\n\n"

	yamlData, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal YAML: %v", err)
	}

	yamlData = append([]byte(description), yamlData...)
	tmpFile, err := os.CreateTemp("", "split-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	tmpFileName := tmpFile.Name()
	if _, err := tmpFile.Write(yamlData); err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %v", err)
	}
	tmpFile.Close()
	return tmpFileName, nil
}

func editYAMLFile(tmpFileName string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	editorParts := strings.Fields(editor)
	var editCmd *exec.Cmd
	if len(editorParts) > 1 {
		editCmd = exec.Command(editorParts[0], append(editorParts[1:], tmpFileName)...)
	} else {
		editCmd = exec.Command(editor, tmpFileName)
	}
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	return editCmd.Run()
}

func readEditedYAMLFile(tmpFileName string) (SplitConfig, error) {
	editedData, err := os.ReadFile(tmpFileName)
	if err != nil {
		return SplitConfig{}, fmt.Errorf("failed to read the edited temporary file: %v", err)
	}
	os.Remove(tmpFileName)

	var editedConfig SplitConfig
	if err := yaml.Unmarshal(editedData, &editedConfig); err != nil {
		return SplitConfig{}, fmt.Errorf("failed to parse edited YAML: %v", err)
	}
	return editedConfig, nil
}

func getCommitLogs(file string) (string, error) {
	cmd := exec.Command("git", "log", "--pretty=format:%s", "--", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error getting commit logs for %s: %v\n", file, err)
		return "", fmt.Errorf("failed to get commit logs for file '%s': %v", file, err)
	}
	return string(out), nil
}

func createBranches(repo *git.Repository, baseCommit *object.Commit, sourceTree *object.Tree, cfg SplitConfig) error {
	headRef, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %v", err)
	}
	currentBranch := headRef.Name().Short()
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %v", err)
	}

	for _, group := range cfg.Branches {
		if len(group.Files) == 0 {
			fmt.Printf("Skipping branch '%s' as there are no target files.\n", group.Name)
			continue
		}
		fmt.Printf("==> Creating branch '%s' (number of target files: %d)\n", group.Name, len(group.Files))

		cmd := exec.Command("git", "add", ".")
		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to add all files to staging: %v", err)
		}

		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(baseBranch),
		}); err != nil {
			return fmt.Errorf("failed to checkout to BASE branch: %v", err)
		}
		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(group.Name),
			Create: true,
			Hash:   baseCommit.Hash,
		}); err != nil {
			return fmt.Errorf("failed to create new branch '%s': %v", group.Name, err)
		}

		for _, file := range group.Files {
			if _, err := sourceTree.File(file); err != nil {
				fmt.Printf("Warning: '%s' does not exist in SOURCE branch.\n", file)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				return fmt.Errorf("failed to create directory '%s': %v", filepath.Dir(file), err)
			}

			fileContent, err := sourceTree.File(file)
			if err != nil {
				return fmt.Errorf("failed to get file '%s' from source tree: %v", file, err)
			}

			fileReader, err := fileContent.Reader()
			if err != nil {
				return fmt.Errorf("failed to get reader for file '%s': %v", file, err)
			}
			defer fileReader.Close()

			fileData, err := io.ReadAll(fileReader)
			if err != nil {
				return fmt.Errorf("failed to read file '%s': %v", file, err)
			}

			if err := os.WriteFile(file, fileData, 0644); err != nil {
				return fmt.Errorf("failed to write file '%s': %v", file, err)
			}
			if _, err := worktree.Add(file); err != nil {
				return fmt.Errorf("failed to add file '%s' to staging: %v", file, err)
			}
			fmt.Printf("Updated: %s\n", file)
		}

		status, err := worktree.Status()
		var commitMsg string
		commitMsgs := []string{}
		for _, file := range group.Files {
			logs, err := getCommitLogs(file)
			if err != nil {
				return err
			}
			commitMsgs = append(commitMsgs, logs)
		}
		commitMsg = strings.Join(commitMsgs, "\n")
		fmt.Printf("Commit message: %s\n", commitMsg)
		if err != nil {
			return fmt.Errorf("failed to get worktree status: %v", err)
		}
		if status.IsClean() {
			fmt.Printf("No changes to commit in branch '%s'. Skipping commit.\n", group.Name)
		} else {
			cmd = exec.Command("git", "commit", "-m", commitMsg)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to commit in branch '%s': %v", group.Name, err)
			}
			fmt.Printf("Committed to branch '%s'\n", group.Name)
		}
	}

	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(currentBranch),
	}); err != nil {
		return fmt.Errorf("failed to checkout back to original branch '%s': %v", currentBranch, err)
	}
	fmt.Printf("Completed. Returned to original branch '%s'.\n", currentBranch)
	return nil
}
