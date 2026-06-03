package storageusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/video-site/backend/internal/mediaasset"
)

func TestComputeCountsLocalThumbnailsAndTeasersByDrive(t *testing.T) {
	localDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(localDir, "thumbs"), 0o755); err != nil {
		t.Fatalf("mkdir thumbs: %v", err)
	}
	writeSizedFile(t, filepath.Join(localDir, "thumbs", "video-a.jpg"), 3)
	writeSizedFile(t, filepath.Join(localDir, "thumbs", "video-b.jpg"), 5)
	longID := "localstorage-" + strings.Repeat("x", 240)
	writeSizedFile(t, mediaasset.ThumbnailPath(localDir, longID), 13)
	teaserA := filepath.Join(localDir, "video-a.mp4")
	teaserB := filepath.Join(localDir, "video-b.mp4")
	writeSizedFile(t, teaserA, 7)
	writeSizedFile(t, teaserB, 11)
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	writeSizedFile(t, outside, 99)

	got, err := Compute(localDir, []VideoAssetRef{
		{ID: "video-a", DriveID: "drive-a", PreviewLocal: teaserA},
		{ID: "video-a-copy", DriveID: "drive-a", PreviewLocal: teaserA},
		{ID: "video-b", DriveID: "drive-b", PreviewLocal: teaserB},
		{ID: longID, DriveID: "drive-b"},
		{ID: "outside", DriveID: "drive-b", PreviewLocal: outside},
		{ID: "unknown-drive-video", DriveID: "missing", PreviewLocal: teaserB},
	}, []string{"drive-a", "drive-b"}, func(string) (DiskStats, error) {
		return DiskStats{AvailableBytes: 123, CapacityBytes: 456}, nil
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	if got.AvailableBytes != 123 || got.CapacityBytes != 456 {
		t.Fatalf("disk stats = available:%d capacity:%d, want 123/456", got.AvailableBytes, got.CapacityBytes)
	}
	driveA := got.Drives["drive-a"]
	if driveA.ThumbnailBytes != 3 || driveA.TeaserBytes != 7 || driveA.TotalBytes != 10 {
		t.Fatalf("drive-a usage = %#v, want thumbnails=3 teaser=7 total=10", driveA)
	}
	driveB := got.Drives["drive-b"]
	if driveB.ThumbnailBytes != 18 || driveB.TeaserBytes != 11 || driveB.TotalBytes != 29 {
		t.Fatalf("drive-b usage = %#v, want thumbnails=18 teaser=11 total=29", driveB)
	}
	if got.ThumbnailBytes != 21 || got.TeaserBytes != 18 || got.TotalBytes != 39 {
		t.Fatalf("totals = %#v, want thumbnails=21 teaser=18 total=39", got)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
