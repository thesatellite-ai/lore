package renderhost

// extra.go — small helpers the moved render code shared with the rest of
// the old binary (perms.go, leak_core_index.go there); copied, not
// imported, because the old binary is a main package.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// meta_keys fields the render reads.
const (
	metaKeyNameField      = "name"
	metaKeyInternalField  = "internal"
	metaKeySensitiveField = "sensitive"
)

// rowBool is a bool field of r (false when absent).
func rowBool(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// writeFileInDir writes data to path through an os.Root opened on path's
// parent folder (P2-LINT-5), so the write lands in that folder and nowhere
// else: a name that is a symlink leading outside the folder is refused
// rather than followed. mode applies only when the file is created.
func writeFileInDir(path string, data []byte, mode fs.FileMode) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(root.WriteFile(filepath.Base(path), data, mode), root.Close())
}

// gitProgram is the git executable looked up on PATH.
const gitProgram = "git"

// runGit runs git with args (LC_ALL=C), returning stdout and, with
// combined, stderr in the same buffer. The Cmd is built directly rather
// than through exec.CommandContext (as wire/host does): bin is the
// absolute path exec.LookPath resolved and every argument is a separate
// argv entry built here, so no shell parses them; ctx ending kills it.
func runGit(ctx context.Context, combined bool, args ...string) ([]byte, error) {
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Env: append(gitenv.Clean(os.Environ()), "LC_ALL=C")}
	var out bytes.Buffer
	cmd.Stdout = &out
	if combined {
		cmd.Stderr = &out
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() }) // a kill of an exited process has nothing to report
	defer stop()
	if err := cmd.Wait(); err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), ctx.Err()
}
