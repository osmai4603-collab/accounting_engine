package posting_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestT2_4_SingleWriterAuthority(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate architecture test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	writeStatement := regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE(?:\s+TABLE)?)\s+(?:public\.)?["']?(?:account_moves|account_move_lines)["']?\b`)
	var violations []string

	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, filepath.Join("storage", "postgres", "posting.go")) {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if writeStatement.Match(source) {
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			violations = append(violations, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("ledger table writes must stay behind the posting persistence adapter: %s", strings.Join(violations, ", "))
	}
}
