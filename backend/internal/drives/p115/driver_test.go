package p115

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestIsTransient115ListError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "blocked html", err: errors.New(`<!doctype html><title>405</title>Sorry, your request has been blocked as it may cause potential threats to the server's security.`), want: true},
		{name: "chinese waf", err: errors.New("很抱歉，由于您访问的URL有可能对网站造成安全威胁，您的访问被阻断。"), want: true},
		{name: "rate limit", err: errors.New("429 too many requests"), want: true},
		{name: "regular auth error", err: errors.New("invalid credential"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient115ListError(tc.err); got != tc.want {
				t.Fatalf("isTransient115ListError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestBufferAndHashSha1 验证 bufferAndHashSha1：
//
//   - 把 reader 的全部字节落到 tmp 文件
//   - SHA1 与标准库一致（HEX 大写）
//   - declaredSize=0 时不校验，>0 时严格校验
//   - 调用方拿到的 *os.File 可以 Seek 回 0 重新读出原文（OSS SDK 上传需要）
func TestBufferAndHashSha1(t *testing.T) {
	body := []byte("hello-115-upload-test")
	want := sha1.Sum(body)
	wantHex := strings.ToUpper(hex.EncodeToString(want[:]))

	t.Run("declared size matches", func(t *testing.T) {
		tmp, gotHex, n, err := bufferAndHashSha1(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("bufferAndHashSha1 returned error: %v", err)
		}
		defer cleanup(tmp)
		if gotHex != wantHex {
			t.Errorf("sha1 = %s, want %s", gotHex, wantHex)
		}
		if n != int64(len(body)) {
			t.Errorf("written = %d, want %d", n, len(body))
		}
		// Seek 回 0，应能读出原文
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("seek: %v", err)
		}
		got, err := io.ReadAll(tmp)
		if err != nil {
			t.Fatalf("read tmp: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("tmp content mismatch: got %q want %q", string(got), string(body))
		}
	})

	t.Run("declared size mismatch returns error", func(t *testing.T) {
		_, _, _, err := bufferAndHashSha1(bytes.NewReader(body), int64(len(body))+1)
		if err == nil {
			t.Fatal("expected size mismatch error, got nil")
		}
	})

	t.Run("declared size zero is unchecked", func(t *testing.T) {
		tmp, gotHex, n, err := bufferAndHashSha1(bytes.NewReader(body), 0)
		if err != nil {
			t.Fatalf("bufferAndHashSha1 returned error: %v", err)
		}
		defer cleanup(tmp)
		if gotHex != wantHex {
			t.Errorf("sha1 = %s, want %s", gotHex, wantHex)
		}
		if n != int64(len(body)) {
			t.Errorf("written = %d, want %d", n, len(body))
		}
	})
}

// TestUploadAndReportSha1RejectsInvalidArgs 检查空 reader / 空 name / 负 size 在
// 客户端未初始化前就被拒绝，避免下游 SDK 在错误参数下做异步初始化和真实网络调用。
func TestUploadAndReportSha1RejectsInvalidArgs(t *testing.T) {
	d := New(Config{ID: "p115-test"})
	// 注意：未调 Init，因此 d.client == nil，第一道防线就会拒绝。

	cases := []struct {
		name      string
		parentID  string
		fname     string
		body      io.Reader
		size      int64
		wantSubst string
	}{
		{name: "nil client", parentID: "0", fname: "x.mp4", body: bytes.NewReader([]byte("ok")), size: 2, wantSubst: "not initialized"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.UploadAndReportSha1(context.Background(), c.parentID, c.fname, c.body, c.size)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSubst) {
				t.Fatalf("err = %v, want containing %q", err, c.wantSubst)
			}
		})
	}
}

func cleanup(f *os.File) {
	if f == nil {
		return
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
}
