//go:build ignore

// Prepare the pinned MyGo dependency with Copied's temporary renderer fix.
// Run from the repository root: go run ./scripts/prepare-mygo.go
package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const version = "v0.9.0"
const sourceHash = "73db5be5d493c62a625f645dfb53a6a2e80337f68df89e5e5f63ff5a51ca3440"

// Retain module@version naming: MyGo uses it to locate the published
// SQLite native binary. Only scene/fold.go changes; native sources stay intact.
const destination = ".mygo-deps/mygo@v0.9.0-fold-v1"
const workspace = "go 1.27.3\n\nuse .\n\nreplace github.com/egoist/mygo v0.9.0 => ./.mygo-deps/mygo@v0.9.0-fold-v1\n"

//go:embed patches/fold-retention_test.go.txt
var regression []byte

func main() {
	if err := prepare(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("MyGo v0.9.0 prepared with the translation-cache retention fix.")
}

func goJSON(args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

func prepare() error {
	if b, err := os.ReadFile("go.work"); err == nil && string(b) != workspace {
		return fmt.Errorf("go.work already exists with different contents; keep your workspace and add the replacement from %s manually", destination)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := goJSON("list", "-m", "-json", "github.com/egoist/mygo")
	if err != nil {
		return err
	}
	var module struct {
		Version, Dir, Error string
		Replace             *json.RawMessage
	}
	if err := json.Unmarshal(out, &module); err != nil {
		return err
	}
	if module.Version != version || module.Replace != nil {
		return fmt.Errorf("expected unmodified MyGo %s in go.mod; review/remove this temporary patch when upgrading", version)
	}
	out, err = goJSON("mod", "download", "-json", "github.com/egoist/mygo@"+version)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, &module); err != nil {
		return err
	}
	if module.Dir == "" || module.Error != "" {
		return fmt.Errorf("download MyGo: %s", module.Error)
	}
	path := filepath.Join("internal", "scene", "fold.go")
	original, err := os.ReadFile(filepath.Join(module.Dir, path))
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(original)) != sourceHash {
		return fmt.Errorf("unexpected MyGo fold.go; refusing to patch changed source")
	}
	before := "func (f *TranslationFolder) fold(s *Scene) *Scene {\n"
	after := before + "\t// Drop the previous scene even when this one needs no folding.\n\t// Clear before reslicing so shorter scenes cannot retain the tail.\n\tclear(f.ops)\n\tf.ops = f.ops[:0]\n\tf.s = Scene{}\n"
	patched := []byte(strings.Replace(string(original), before, after, 1))
	if existing, err := os.ReadFile(filepath.Join(destination, path)); err == nil {
		test, testErr := os.ReadFile(filepath.Join(destination, "internal", "scene", "copied_fold_retention_test.go"))
		if !bytes.Equal(existing, patched) || testErr != nil || !bytes.Equal(test, regression) {
			return fmt.Errorf("%s differs from the expected generated dependency; move it aside and rerun", destination)
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err := os.MkdirAll(".mygo-deps", 0755); err != nil {
			return err
		}
		temp, err := os.MkdirTemp(".mygo-deps", "prepare-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)
		if err := os.CopyFS(temp, os.DirFS(module.Dir)); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(temp, path), patched, 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(temp, "internal", "scene", "copied_fold_retention_test.go"), regression, 0644); err != nil {
			return err
		}
		if err := os.Rename(temp, destination); err != nil {
			return err
		}
	}
	// Keep an existing user workspace intact; only create our own exact file.
	if _, err := os.Stat("go.work"); os.IsNotExist(err) {
		return os.WriteFile("go.work", []byte(workspace), 0644)
	} else {
		return err
	}
}
