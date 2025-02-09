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
	Run: func(cmd *cobra.Command, args []string) {

		// Open repository from current directory
		repo, err := git.PlainOpen(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Critical Error: Failed to open repository\n")
			fmt.Fprintf(os.Stderr, "Error details: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Repository opened successfully")

		// Display available branches
		refs, _ := repo.References()
		fmt.Println("\nAvailable branches:")
		_ = refs.ForEach(func(ref *plumbing.Reference) error {
			if ref.Name().IsBranch() {
				fmt.Printf("- %s\n", ref.Name().Short())
			}
			return nil
		})

		// Get the latest commit and tree from BASE branch
		fmt.Printf("Getting reference for BASE branch '%s'...\n", baseBranch)
		baseRef, err := repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
		if err != nil {
			// Display available branches
			refs, _ := repo.References()
			fmt.Println("\nAvailable branches:")
			_ = refs.ForEach(func(ref *plumbing.Reference) error {
				if ref.Name().IsBranch() {
					fmt.Printf("- %s\n", ref.Name().Short())
				}
				return nil
			})
			log.Fatalf("\nFailed to get reference for BASE branch '%s': %v", baseBranch, err)
		}
		fmt.Printf("Successfully got reference for BASE branch '%s'\n", baseBranch)
		baseCommit, err := repo.CommitObject(baseRef.Hash())
		if err != nil {
			log.Fatalf("Failed to get commit for BASE branch: %v", err)
		}
		fmt.Fprintf(os.Stderr, "BASE commit hash: %s\n", baseRef.Hash())
		baseTree, err := baseCommit.Tree()
		if err != nil {
			log.Fatalf("Failed to get tree for BASE branch: %v", err)
		}

		// Get the latest commit and tree from SOURCE branch
		fmt.Printf("Getting reference for SOURCE branch '%s'...\n", sourceBranch)
		sourceRef, err := repo.Reference(plumbing.NewBranchReferenceName(sourceBranch), true)
		if err != nil {
			// Display available branches
			refs, _ := repo.References()
			fmt.Println("\nAvailable branches:")
			_ = refs.ForEach(func(ref *plumbing.Reference) error {
				if ref.Name().IsBranch() {
					fmt.Printf("- %s\n", ref.Name().Short())
				}
				return nil
			})
			log.Fatalf("\nFailed to get reference for SOURCE branch '%s': %v", sourceBranch, err)
		}
		fmt.Printf("Successfully got reference for SOURCE branch '%s'\n", sourceBranch)
		sourceCommit, err := repo.CommitObject(sourceRef.Hash())
		if err != nil {
			log.Fatalf("Failed to get commit for SOURCE branch: %v", err)
		}
		fmt.Fprintf(os.Stderr, "SOURCE commit hash: %s\n", sourceRef.Hash())
		sourceTree, err := sourceCommit.Tree()
		if err != nil {
			log.Fatalf("Failed to get tree for SOURCE branch: %v", err)
		}

		// Get the tree differences between BASE and SOURCE (only added/modified files)
		changes, err := baseTree.Diff(sourceTree)
		if err != nil {
			log.Fatalf("Failed to get diff: %v", err)
		}

		// Create a list of diff files (excluding deletions)
		fileSet := make(map[string]bool)
		var diffFiles []string
		for _, change := range changes {
			action, err := change.Action()
			if err != nil {
				log.Fatalf("Failed to get action for change: %v", err)
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
		fmt.Printf("Diff files count between BASE branch '%s' and SOURCE branch '%s': %d\n", baseBranch, sourceBranch, totalFiles)
		if totalFiles == 0 {
			fmt.Println("No diff files found.")
			return
		}

		// Group the list of files into chunks of the specified size and add them to the SplitConfig struct
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

		// Add description to the YAML data
		description := "# This YAML file contains the configuration for splitting branches.\n" +
			"# Each branch group specifies a branch name and the list of files to be included in that branch.\n\n"

		// Output the combined YAML to a temporary file
		yamlData, err := yaml.Marshal(&cfg)
		if err != nil {
			log.Fatalf("Failed to marshal YAML: %v", err)
		}

		// Prepend the description to the YAML data
		yamlData = append([]byte(description), yamlData...)

		tmpFile, err := os.CreateTemp("", "split-config-*.yaml")
		if err != nil {
			log.Fatalf("Failed to create temporary file: %v", err)
		}
		tmpFileName := tmpFile.Name()
		if _, err := tmpFile.Write(yamlData); err != nil {
			log.Fatalf("Failed to write to temporary file: %v", err)
		}
		tmpFile.Close()

		// Edit the temporary YAML file with the EDITOR (default is "vi")
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		// Split the editor command and options
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
		if err := editCmd.Run(); err != nil {
			log.Fatalf("Failed to launch editor: %v", err)
		}

		// Read and parse the edited YAML
		editedData, err := os.ReadFile(tmpFileName)
		if err != nil {
			log.Fatalf("Failed to read the edited temporary file: %v", err)
		}
		os.Remove(tmpFileName)

		var editedConfig SplitConfig
		if err := yaml.Unmarshal(editedData, &editedConfig); err != nil {
			log.Fatalf("Failed to parse edited YAML: %v", err)
		}

		// Save the current branch name
		headRef, err := repo.Head()
		if err != nil {
			log.Fatalf("Failed to get HEAD: %v", err)
		}
		currentBranch := headRef.Name().Short()
		// Get the worktree
		worktree, err := repo.Worktree()
		if err != nil {
			log.Fatalf("Failed to get worktree: %v", err)
		}

		// Create a new branch for each group and update with the contents from the SOURCE branch
		for _, group := range editedConfig.Branches {
			// Skip if the file list is empty after editing
			if len(group.Files) == 0 {
				fmt.Printf("Skipping branch '%s' as there are no target files.\n", group.Name)
				continue
			}
			fmt.Printf("==> Creating branch '%s' (number of target files: %d)\n", group.Name, len(group.Files))

			// Checkout to the BASE branch before creating a new branch
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewBranchReferenceName(baseBranch),
			})
			if err != nil {
				log.Fatalf("Failed to checkout to BASE branch: %v", err)
			}
			err = worktree.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewBranchReferenceName(group.Name),
				Create: true,
				Hash:   baseCommit.Hash,
			})
			if err != nil {
				log.Fatalf("Failed to create new branch '%s': %v", group.Name, err)
			}

			// Overwrite each file with the contents from the SOURCE branch
			for _, file := range group.Files {
				// Check if the file exists in the SOURCE branch
				_, err := sourceTree.File(file)
				if err != nil {
					fmt.Printf("Warning: '%s' does not exist in SOURCE branch.\n", file)
					continue
				}
				// Create necessary directories
				if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					log.Fatalf("Failed to create directory '%s': %v", filepath.Dir(file), err)
				}

				// Checkout the target file from the SOURCE branch using go-git
				fileContent, err := sourceTree.File(file)
				if err != nil {
					log.Fatalf("Failed to get file '%s' from source tree: %v", file, err)
				}

				fileReader, err := fileContent.Reader()
				if err != nil {
					log.Fatalf("Failed to get reader for file '%s': %v", file, err)
				}
				defer fileReader.Close()

				fileData, err := io.ReadAll(fileReader)
				if err != nil {
					log.Fatalf("Failed to read file '%s': %v", file, err)
				}

				if err := os.WriteFile(file, fileData, 0644); err != nil {
					log.Fatalf("Failed to write file '%s': %v", file, err)
				}
				// Add to the staging area
				if _, err := worktree.Add(file); err != nil {
					log.Fatalf("Failed to add file '%s' to staging: %v", file, err)
				}
				fmt.Printf("Updated: %s\n", file)
			}

			// Commit if there are staged changes
			status, err := worktree.Status()
			if err != nil {
				log.Fatalf("Failed to get worktree status: %v", err)
			}
			if status.IsClean() {
				fmt.Printf("No changes to commit in branch '%s'. Skipping commit.\n", group.Name)
			} else {
				commitMsg := fmt.Sprintf("Update diff files: %v", group.Files)
				repoConfig, _ := repo.Config()
				commitOptions := &git.CommitOptions{
					Author: &object.Signature{
						Name:  repoConfig.User.Name,
						Email: repoConfig.User.Email,
						When:  sourceCommit.Author.When,
					},
				}
				commitHash, err := worktree.Commit(commitMsg, commitOptions)
				if err != nil {
					log.Fatalf("Failed to commit in branch '%s': %v", group.Name, err)
				}
				fmt.Printf("Committed to branch '%s': %s\n", group.Name, commitHash)
			}
		}

		// Finally, checkout back to the original branch
		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(currentBranch),
		}); err != nil {
			log.Fatalf("Failed to checkout back to original branch '%s': %v", currentBranch, err)
		}
		fmt.Printf("Completed. Returned to original branch '%s'.\n", currentBranch)
	},
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
