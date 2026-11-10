// Package rowfs is the row-file layout both file-based backends share (the
// folder backend and the git backend): one canonical JSON file per row at
// <store root>/<folder>/.lore/data/<table>/<id>.json, written atomically,
// guarded by an exclusive store lock, and change-detected with a stat cursor.
//
// Why one package: the folder and git backends must lay rows out byte for
// byte the same way, so a folder can become a git store with `git init` and
// a git store's worktree can be read as a folder (STORE_SERVER_SPEC 6.1,
// 6.2). Everything that touches row files lives here once.
//
// Folders (owner decision O1). A store is a root plus nested folders, each
// with its own .lore/data: the root folder ("."), project folders
// ("acme-hub") and the company-wide folder ("org"). A Root is the store; a
// Folder is one of those data folders, chosen by a slash-separated relative
// name (FolderRoot for the root). Backends address one folder per Backend.
//
// Ported from saas/pkg/aicoder/lsync/files.go (layout, size cap, racy
// window, atomic write), saas/pkg/aicoder/guard (network file system check)
// and saas/pkg/aicoder/processlock (OS file lock).
//
// Layer L3 (adapters): imports model and store only.
package rowfs
