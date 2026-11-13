package git

import "testing"

// TestDrivePathStoreURL (B3-27): a Windows drive path as a store url is a
// local git repository on every OS, never a url with scheme "c": lore
// carries it as the url path "/C:/…", keys its clone under _local, and
// hands git the drive form "C:/…".
func TestDrivePathStoreURL(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`C:\Users\nina\lore-notes.git`, `C:/Users/nina/lore-notes.git`} {
		canon, err := CanonicalURL(in)
		if err != nil || canon != "/C:/Users/nina/lore-notes.git" {
			t.Fatalf("CanonicalURL(%q) = %q, %v", in, canon, err)
		}
		r, err := RemoteOfRaw(in)
		if err != nil || r.Norm != LocalHost+"/C:/Users/nina/lore-notes" || !r.isLocal() {
			t.Fatalf("RemoteOfRaw(%q) = %+v, %v", in, r, err)
		}
		if o, err := OriginOf(in); err != nil || o != "C:/Users/nina/lore-notes.git" {
			t.Fatalf("OriginOf(%q) = %q, %v; want the drive form git reads", in, o, err)
		}
	}
}
