package catalog

import (
	"context"
	"database/sql"
	"sort"
	"testing"
	"time"
)

// TestListVideoFileIDsByDrive 校验 spider91 crawler 用到的轻量 file_id 查询：
// - 只返回指定 drive 的 file_id；不返回其它 drive 的
// - 跳过 file_id 为空的视频
// - 返回顺序无要求，但每个 file_id 只出现一次
func TestListVideoFileIDsByDrive(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	insert := func(id, drive, fileID string) {
		if err := cat.UpsertVideo(ctx, &Video{
			ID:          id,
			DriveID:     drive,
			FileID:      fileID,
			Title:       id,
			PublishedAt: now,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	insert("spider91-A-vk001", "spider-a", "vk001.mp4")
	insert("spider91-A-vk002", "spider-a", "vk002.flv")
	insert("spider91-A-vk003", "spider-a", "vk003.mp4")
	// 不同 drive 的视频不应出现
	insert("quark-other-fid", "drive-quark", "abcdef")
	// 空 file_id 应被过滤
	insert("spider91-A-empty", "spider-a", "")

	got, err := cat.ListVideoFileIDsByDrive(ctx, "spider-a")
	if err != nil {
		t.Fatalf("ListVideoFileIDsByDrive: %v", err)
	}
	sort.Strings(got)
	want := []string{"vk001.mp4", "vk002.flv", "vk003.mp4"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %d ids, want %d: got=%v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 空 drive 返回空列表，不报错
	other, err := cat.ListVideoFileIDsByDrive(ctx, "no-such-drive")
	if err != nil {
		t.Fatalf("ListVideoFileIDsByDrive empty: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("non-existent drive: got %v, want empty", other)
	}
}

// TestListSpider91ViewkeysFindsMigratedVideos 校验：即使 spider91 视频
// 被迁移到 PikPak（drive_id 改了），ListSpider91Viewkeys 仍能通过 video.id
// 前缀找到这些 viewkey。这是 crawler 写 seen 文件的关键不变量，
// 否则下一次爬取会把已爬过的 viewkey 当作"新"的再爬一遍。
func TestListSpider91ViewkeysFindsMigratedVideos(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	insert := func(id, drive, fileID string) {
		if err := cat.UpsertVideo(ctx, &Video{
			ID:          id,
			DriveID:     drive,
			FileID:      fileID,
			Title:       id,
			PublishedAt: now,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	// 1) 仍在 spider91 drive 下的视频（未迁移）
	insert("spider91-91Spider-vk001", "91Spider", "vk001.mp4")
	// 2) 已迁移到 PikPak 的视频：drive_id 变了，但 id 仍是 spider91-91Spider-...
	insert("spider91-91Spider-vk002", "PikPak", "PIKPAK-FILE-ID-2")
	insert("spider91-91Spider-vk003", "PikPak", "PIKPAK-FILE-ID-3")
	// 3) 别的 spider91 drive 的视频，不应混进来
	insert("spider91-OtherDrive-vk999", "OtherDrive", "vk999.mp4")
	// 4) 完全无关的视频
	insert("quark-some-fid", "drive-quark", "abc")

	got, err := cat.ListSpider91Viewkeys(ctx, "91Spider")
	if err != nil {
		t.Fatalf("ListSpider91Viewkeys: %v", err)
	}
	sort.Strings(got)
	want := []string{"vk001", "vk002", "vk003"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %d viewkeys, want %d: got=%v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 不存在的 drive 返回空列表
	other, err := cat.ListSpider91Viewkeys(ctx, "no-such-drive")
	if err != nil {
		t.Fatalf("ListSpider91Viewkeys empty: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("non-existent drive: got %v, want empty", other)
	}
}

func TestDeleteVideoWithTombstonePreventsReimport(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:            "spider91-91Spider-vk004",
		DriveID:       "91Spider",
		FileID:        "vk004.mp4",
		FileName:      "vk004.mp4",
		ContentHash:   "ABCDEF",
		Title:         "Deleted Spider",
		Size:          2048,
		PreviewStatus: "ready",
		PublishedAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := cat.DeleteVideoWithTombstone(ctx, "spider91-91Spider-vk004"); err != nil {
		t.Fatalf("delete with tombstone: %v", err)
	}
	if _, err := cat.GetVideo(ctx, "spider91-91Spider-vk004"); err != sql.ErrNoRows {
		t.Fatalf("get deleted video error = %v, want sql.ErrNoRows", err)
	}
	deleted, err := cat.IsDeletedVideoCandidate(ctx, "spider91-91Spider-vk004", "91Spider", "vk004.mp4", "abcdef", "vk004.mp4", 2048)
	if err != nil {
		t.Fatalf("check deleted candidate: %v", err)
	}
	if !deleted {
		t.Fatal("deleted candidate was not recognized")
	}
	viewkeys, err := cat.ListSpider91Viewkeys(ctx, "91Spider")
	if err != nil {
		t.Fatalf("ListSpider91Viewkeys: %v", err)
	}
	if len(viewkeys) != 1 || viewkeys[0] != "vk004" {
		t.Fatalf("viewkeys = %#v, want [vk004]", viewkeys)
	}
}
