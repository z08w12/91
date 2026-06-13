package catalog

import (
	"context"
	"testing"
	"time"
)

// TestListHiddenVideosForMigration 验证：隐藏的视频不进可见列表，
// 但能被 ListHiddenVideos 拿到（供一次性迁移为墓碑）。
func TestListHiddenVideosForMigration(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	for _, id := range []string{"v1", "v2", "v3"} {
		if err := cat.UpsertVideo(ctx, &Video{
			ID: id, DriveID: "drive", FileID: "f-" + id, Title: id,
			PublishedAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := cat.HideVideo(ctx, "v2"); err != nil {
		t.Fatalf("hide v2: %v", err)
	}

	visible, total, err := cat.ListVideos(ctx, ListParams{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("list visible: %v", err)
	}
	if total != 2 || len(visible) != 2 {
		t.Fatalf("visible total/len = %d/%d, want 2/2", total, len(visible))
	}
	for _, v := range visible {
		if v.ID == "v2" {
			t.Fatalf("hidden v2 leaked into visible list")
		}
	}

	hidden, err := cat.ListHiddenVideos(ctx)
	if err != nil {
		t.Fatalf("list hidden: %v", err)
	}
	if len(hidden) != 1 || hidden[0].ID != "v2" {
		t.Fatalf("ListHiddenVideos = %v, want only v2", hidden)
	}

	current, blacklisted, err := cat.VideoManagementCounts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if current != 2 || blacklisted != 0 {
		t.Fatalf("counts = current %d blacklisted %d, want 2/0", current, blacklisted)
	}
}

// TestBlacklistListAndRemove 验证墓碑表的列出、关键字过滤和移除。
func TestBlacklistListAndRemove(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	seed := []struct{ id, file string }{
		{"d1", "movie-alpha.avi"},
		{"d2", "movie-beta.mp4"},
		{"d3", "clip-gamma.wmv"},
	}
	for _, s := range seed {
		if err := cat.UpsertVideo(ctx, &Video{
			ID: s.id, DriveID: "drive", FileID: "f-" + s.id, FileName: s.file,
			Title: s.id, PublishedAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
		if err := cat.DeleteVideoWithTombstone(ctx, s.id); err != nil {
			t.Fatalf("tombstone %s: %v", s.id, err)
		}
	}

	items, total, err := cat.ListDeletedVideos(ctx, "", 1, 50)
	if err != nil {
		t.Fatalf("list deleted: %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("deleted total/len = %d/%d, want 3/3", total, len(items))
	}

	// 关键字过滤
	filtered, ftotal, err := cat.ListDeletedVideos(ctx, "movie", 1, 50)
	if err != nil {
		t.Fatalf("list deleted filtered: %v", err)
	}
	if ftotal != 2 || len(filtered) != 2 {
		t.Fatalf("filtered total/len = %d/%d, want 2/2", ftotal, len(filtered))
	}

	// 移出黑名单
	if err := cat.RemoveDeletedVideo(ctx, "d1"); err != nil {
		t.Fatalf("remove d1: %v", err)
	}
	if deleted, err := cat.IsVideoDeleted(ctx, "d1"); err != nil || deleted {
		t.Fatalf("d1 should no longer be blacklisted (deleted=%v err=%v)", deleted, err)
	}
	_, total, err = cat.ListDeletedVideos(ctx, "", 1, 50)
	if err != nil {
		t.Fatalf("list deleted after remove: %v", err)
	}
	if total != 2 {
		t.Fatalf("deleted total after remove = %d, want 2", total)
	}

	if err := cat.RemoveDeletedVideo(ctx, "does-not-exist"); err == nil {
		t.Fatalf("remove missing id should return error")
	}

	// counts: 删完一个还剩 2 个黑名单；可见视频已全部被墓碑删除
	current, blacklisted, err := cat.VideoManagementCounts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if current != 0 || blacklisted != 2 {
		t.Fatalf("counts = current %d blacklisted %d, want 0/2", current, blacklisted)
	}
}
