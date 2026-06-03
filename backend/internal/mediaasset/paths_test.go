package mediaasset

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFilenamesKeepShortSafeIDs(t *testing.T) {
	if got := ThumbnailFilename("video-1"); got != "video-1.jpg" {
		t.Fatalf("thumbnail filename = %q, want video-1.jpg", got)
	}
	if got := PreviewFilename("video-1"); got != "video-1.mp4" {
		t.Fatalf("preview filename = %q, want video-1.mp4", got)
	}
}

func TestFilenamesHashLongOrUnsafeIDs(t *testing.T) {
	longID := "localstorage-" + strings.Repeat("x", 240)
	got := ThumbnailFilename(longID)
	if !strings.HasPrefix(got, "v-") || !strings.HasSuffix(got, ".jpg") {
		t.Fatalf("thumbnail filename = %q, want hashed jpg", got)
	}
	if len([]byte(got)) >= len([]byte(longID+".jpg")) {
		t.Fatalf("thumbnail filename = %q should be shorter than original id", got)
	}

	unsafe := ThumbnailFilename("dir/video")
	if unsafe == "dir/video.jpg" || strings.ContainsAny(unsafe, `/\`) {
		t.Fatalf("unsafe thumbnail filename = %q, want hashed single filename", unsafe)
	}
}

func TestThumbnailPathCandidatesIncludeLegacyForHashedIDs(t *testing.T) {
	localDir := t.TempDir()
	mediumID := "localstorage-" + strings.Repeat("x", 190)
	got := ThumbnailPathCandidates(localDir, mediumID)
	if len(got) != 2 {
		t.Fatalf("candidates = %#v, want hashed and legacy paths", got)
	}
	if got[0] != ThumbnailPath(localDir, mediumID) {
		t.Fatalf("first candidate = %q, want safe path %q", got[0], ThumbnailPath(localDir, mediumID))
	}
	if filepath.Base(got[1]) != mediumID+".jpg" {
		t.Fatalf("legacy candidate = %q, want original id jpg", got[1])
	}
}

func TestThumbnailPathCandidatesSkipOverlongLegacy(t *testing.T) {
	localDir := t.TempDir()
	longID := "localstorage-" + strings.Repeat("x", 240)
	got := ThumbnailPathCandidates(localDir, longID)
	if len(got) != 1 {
		t.Fatalf("candidates = %#v, want only hashed path for overlong id", got)
	}
}
