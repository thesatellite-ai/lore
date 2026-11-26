package git

// authoritystream.go — the full roster replay read as a stream (bug hunt
// H-279, devco:D-1017). The first open of a clone (the first `lore sync`
// after a clone, the first command after a lore upgrade) replays the whole
// history. Before, it read git's whole log of every commit into one
// string, kept every commit's file list, read every row version of the
// history at once and decoded each into memory before deciding the first
// commit: at 100k commits ~1.5 GB, at 1M commits ~4 GB and ~38 minutes.
// The replay's identifiers also pointed into that one log string, so the
// whole log stayed in memory for as long as the replay did.
//
// Now the log is read as git prints it and decided a chunk of commits at
// a time (replayChunkCommits): one cat-file read per chunk for the row
// versions its commits wrote, decoded, decided, and dropped. Each kept
// identifier is a copy of its own. Memory is the replay's own result plus
// one chunk, whatever the history length; the decisions are the same,
// since each commit is decided with exactly the blobs a whole-history read
// gave it (replayOver reads, per chunk, every blob its commits name plus
// the version each pinnable file had before the chunk).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/adapters/gitfast"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// replayChunkCommits is how many commits the replay decides per round of
// blob reads: big enough that a long history starts few git processes
// (100 at 400k commits), small enough that one chunk's decoded row
// versions are a few MB. A var: a test decides one commit per chunk.
var replayChunkCommits = 4096

// replayBlobsPeak is the most row versions one replay read decoded at
// once in this process: the tests' proof that a long history is never
// read whole (bug hunt H-279, devco:D-1017).
var replayBlobsPeak atomic.Int64

// notePeak raises replayBlobsPeak to n.
func notePeak(n int) {
	for {
		was := replayBlobsPeak.Load()
		if int64(n) <= was || replayBlobsPeak.CompareAndSwap(was, int64(n)) {
			return
		}
	}
}

// replayLogArgs is the git log the replay reads: oldest first in the
// order the replay decides commits, every file each commit changed with
// its blob ids on both sides (a merge: the files whose merged version
// neither parent had, git log -c), records started by commitMark.
var replayLogArgs = []string{"log", "--reverse", "--topo-order", "-c", "--no-renames", "--raw", "--no-abbrev", "-z", "--format=" + "%x01%H%x00%P%x00%ae%x00"}

// parseReplayRecord is one commit of the replay log (the text after its
// commitMark); ok is false for a record that is not one. Every string it
// keeps is a copy, so the replay never holds the log text it came from.
func parseReplayRecord(rec string) (replayCommit, bool) {
	parts := strings.SplitN(rec, "\x00", 4)
	if len(parts) < 4 {
		return replayCommit{}, false
	}
	c := replayCommit{id: strings.Clone(parts[0]), author: strings.ToLower(strings.Clone(parts[2]))}
	for _, p := range strings.Fields(parts[1]) {
		c.parents = append(c.parents, strings.Clone(p))
	}
	fields := strings.Split(strings.TrimLeft(parts[3], "\x00\n"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		// ":<modes…> <blobs…> <status>": one colon per parent, then
		// parents+1 modes and parents+1 blobs (the last is the result)
		raw := strings.TrimSpace(fields[i])
		n := len(raw) - len(strings.TrimLeft(raw, ":"))
		meta := strings.Fields(strings.TrimLeft(raw, ":"))
		if n < 1 || len(meta) < 2*n+3 {
			continue
		}
		c.merge = c.merge || n > 1
		c.changes = append(c.changes, fileChange{path: strings.Clone(fields[i+1]), oldBlob: strings.Clone(meta[n+1]), newBlob: strings.Clone(meta[2*n+1])})
	}
	return c, true
}

// fullReplay is the replay of head's whole history, read and decided a
// chunk at a time (streamReplayLog). Why (bug hunt H-279, devco:D-1017):
// see the file comment.
func (b *Backend) fullReplay(ctx context.Context, head string) (*authority, error) {
	paceReplayGC()
	a, err := b.newReplay(ctx, head)
	if err != nil {
		return nil, err
	}
	first := true
	err = b.streamReplayLog(ctx, head, func(chunk []replayCommit) error {
		if first {
			first = false
			b.markStreamedSnapshotRoot(ctx, head, chunk)
		}
		return b.replayOver(ctx, a, chunk)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// markStreamedSnapshotRoot is markSnapshotRoot for a replay that sees the
// history a chunk at a time: the first commit is the root the log lists
// first; it is marked only when it carries TrailerSnapshot and the history
// has no other root (asked of git only then, so a store that was never
// compacted pays nothing for it).
func (b *Backend) markStreamedSnapshotRoot(ctx context.Context, head string, chunk []replayCommit) {
	if len(chunk) == 0 || len(chunk[0].parents) != 0 {
		return
	}
	for _, c := range chunk[1:] {
		if len(c.parents) == 0 {
			return // two roots in the first chunk: none is trusted
		}
	}
	m, err := b.metaOf(ctx, chunk[0].id)
	if err != nil || trailerValue(m.msg, TrailerSnapshot) == "" {
		return
	}
	roots, err := b.lines(ctx, "find the history's roots", "rev-list", "--max-parents=0", head)
	if err != nil || len(roots) != 1 || roots[0] != chunk[0].id {
		return
	}
	chunk[0].snapshot = true
}

// streamReplayLog reads the replay log of rev (replayLogArgs) and hands
// each chunk of replayChunkCommits commits, oldest first, to each. git
// prints the next chunks while each decides one (replayReadAhead chunks
// ahead at most), so the log and the replay run side by side. A log
// gitfast already holds is read from there; else git runs with its
// output read as it comes, never held whole. An error of each stops git
// and comes back as each made it.
func (b *Backend) streamReplayLog(ctx context.Context, rev string, each func([]replayCommit) error) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := append(append([]string(nil), replayLogArgs...), rev, "--")
	chunks := make(chan []replayCommit, replayReadAhead)
	var serr error // set before chunks closes
	go func() {
		defer close(chunks)
		send := func(chunk []replayCommit) error {
			select {
			case chunks <- chunk:
				return nil
			case <-sctx.Done():
				return sctx.Err()
			}
		}
		var chunk []replayCommit
		serr = b.git.stream(sctx, call{dir: b.work, args: args}, commitMark[0], func(rec []byte) error {
			c, ok := parseReplayRecord(string(rec))
			if !ok {
				return nil
			}
			chunk = append(chunk, c)
			if len(chunk) < replayChunkCommits {
				return nil
			}
			full := chunk
			chunk = nil
			return send(full)
		})
		if serr == nil && len(chunk) > 0 {
			serr = send(chunk)
		}
	}()
	var cerr error
	for chunk := range chunks {
		if cerr != nil {
			continue // git is being stopped: drain what it sent
		}
		if cerr = each(chunk); cerr != nil {
			cancel()
		}
	}
	switch {
	case cerr != nil:
		return cerr
	case serr != nil:
		return errs.Wrap(errs.Internal, "git: read the store's history for the roster check", serr)
	}
	return nil
}

// replayReadAhead is how many decoded chunks git's log may be ahead of
// the replay.
const replayReadAhead = 2

// streamKeepMax is the largest streamed output stream hands to gitfast to
// keep (gitfast keeps answers up to 8 MB; a bigger one is never kept, so
// it is not held for it either).
const streamKeepMax = 8 << 20

// stream runs git like run and hands its output to each one record at a
// time, records ended by sep (the separator itself left out). Why (bug
// hunt H-279, devco:D-1017): the replay log of a long history is hundreds
// of MB; run holds the whole of it. A read gitfast answers (the log a
// process already read) is split from its answer. An error each returns
// stops git and comes back as is.
func (r runner) stream(ctx context.Context, c call, sep byte, each func([]byte) error) error {
	args := r.argsOf(c)
	if why, refused := gitenv.StoreNetRefused(os.Environ(), c.args); refused {
		return &gitError{cmd: gitenv.Subcommand(c.args), code: netRefusedExit, stderr: why}
	}
	split := func(out []byte) error {
		for len(out) > 0 {
			i := bytes.IndexByte(out, sep)
			if i < 0 {
				return each(out)
			}
			if err := each(out[:i]); err != nil {
				return err
			}
			out = out[i+1:]
		}
		return nil
	}
	direct := len(r.routeArgs) == 0 && len(r.routeEnv) == 0
	if direct {
		if rep, ok := gitfast.Try(ctx, c.dir, args, c.env, c.stdin); ok {
			if rep.Code != 0 {
				return &gitError{cmd: subcommand(c.args), code: rep.Code, stderr: rep.Stderr}
			}
			return split(rep.Out)
		}
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := &exec.Cmd{Path: r.bin, Args: append([]string{r.bin}, args...), Dir: c.dir}
	cmd.Env = append(gitenv.Clean(os.Environ()), envLocale, envNoPrompt)
	cmd.Env = append(cmd.Env, r.routeEnv...)
	cmd.Env = append(cmd.Env, c.env...)
	if c.stdin != nil {
		cmd.Stdin = bytes.NewReader(c.stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := sctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-sctx.Done():
			if kerr := cmd.Process.Kill(); kerr != nil {
				return // it already exited; Wait reports how
			}
		case <-done:
		}
	}()
	// keep is the output so far while it is small enough for gitfast to
	// keep (streamKeepMax), so a second read in this process is answered
	// without git, as run's are
	var keep []byte
	keeping := direct
	rd := bufio.NewReaderSize(pipe, 1<<16)
	var serr error
	for serr == nil {
		rec, rerr := rd.ReadBytes(sep)
		if keeping {
			if len(keep)+len(rec) > streamKeepMax {
				keeping, keep = false, nil
			} else {
				keep = append(keep, rec...)
			}
		}
		if len(rec) > 0 && rec[len(rec)-1] == sep {
			rec = rec[:len(rec)-1]
		}
		if len(rec) > 0 {
			serr = each(rec)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil && serr == nil {
			serr = rerr
		}
	}
	if serr != nil {
		cancel()
		_, _ = io.Copy(io.Discard, pipe)
	}
	werr := cmd.Wait()
	close(done)
	if serr != nil {
		return serr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			return &gitError{cmd: subcommand(c.args), code: ee.ExitCode(), stderr: stderr.String()}
		}
		return werr
	}
	if keeping {
		gitfast.Note(ctx, c.dir, args, c.env, c.stdin, keep, 0, true)
	}
	return nil
}

// replayGCPercent is the collector's pace once a long replay ran in the
// process: a collection when the heap grew half past what the last one
// kept. Why (bug hunt H-279, devco:D-1017): the CLI lets the heap grow five
// times past it (app gcPercent 400), which suits a command of a tenth of
// a second with a small heap; a long history's replay stays in memory for
// the rest of the process (authorityCache), ~550 MB at 1M commits, so the
// heap grew to several GB before collecting. At 50 the replay of 1M
// commits peaks under 1 GB RSS, for a few seconds more CPU over minutes.
const replayGCPercent = 50

// paceReplayGC sets replayGCPercent for the rest of the process. A pace
// already at or below it (a person's GOGC, the read server's, or
// GOGC=off) is left as it is.
func paceReplayGC() {
	if prev := debug.SetGCPercent(replayGCPercent); prev <= replayGCPercent {
		debug.SetGCPercent(prev)
	}
}
