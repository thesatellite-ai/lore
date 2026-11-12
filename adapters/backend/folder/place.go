package folder

// Dir is the store root on disk; lore migrate backs up and restores the
// data folder under it (FORMAT_EVOLUTION rule 4).
func (b *Backend) Dir() string { return b.root.Dir() }

// DataPath is the data folder's slash path under Dir (rowfs Folder.DataPath:
// "." for a store's root folder, which also holds the folders below it).
func (b *Backend) DataPath() string { return b.folder.DataPath() }
