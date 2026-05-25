package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  Server  `yaml:"server"`
	Storage Storage `yaml:"storage"`
	Scanner Scanner `yaml:"scanner"`
	Preview Preview `yaml:"preview"`
	Drives  []Drive `yaml:"drives"`
}

type Server struct {
	Listen        string `yaml:"listen"`
	Admin         Admin  `yaml:"admin"`
	SessionSecret string `yaml:"session_secret"`
	// AllowedOrigins 是允许跨源访问的前端 Origin 白名单（如 "https://video.example.com"）。
	// 默认空 → 不开启 CORS 跨源；同源部署（前后端在同一个域名 + 端口下）不需要配置此项。
	// 浏览器对不在列表里的 Origin 不会拿到 Access-Control-Allow-Origin 头，自然就读不到响应。
	// 不要写 "*"；带 cookie 的 CORS 必须是具体 Origin。
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type Admin struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Storage struct {
	DBPath          string `yaml:"db_path"`
	LocalPreviewDir string `yaml:"local_preview_dir"`
}

type Scanner struct {
	IntervalSeconds int      `yaml:"interval_seconds"`
	MaxDepth        int      `yaml:"max_depth"`
	VideoExtensions []string `yaml:"video_extensions"`
}

type Preview struct {
	Enabled         bool   `yaml:"enabled"`
	FFmpegPath      string `yaml:"ffmpeg_path"`
	FFprobePath     string `yaml:"ffprobe_path"`
	DurationSeconds int    `yaml:"duration_seconds"`
	Width           int    `yaml:"width"`
	Segments        int    `yaml:"segments"`
}

// Drive 配置项中的敏感字段（Cookie / RefreshToken 等）最终由管理后台写入 DB
// 这里保留 yaml 中的静态定义，用于启动时预置盘。生产建议只在 DB 里维护。
type Drive struct {
	ID     string            `yaml:"id"`
	Kind   string            `yaml:"kind"` // quark / p115 / pikpak / wopan / onedrive
	Name   string            `yaml:"name"`
	RootID string            `yaml:"root_id"`
	Params map[string]string `yaml:"params,omitempty"`
}

// Load 读取配置；若不存在则从 config.example.yaml 复制一份并返回
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		example := filepath.Join(filepath.Dir(path), "config.example.yaml")
		data, err := os.ReadFile(example)
		if err != nil {
			return nil, fmt.Errorf("config not found and example missing: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = "./data/video-site.db"
	}
	if c.Storage.LocalPreviewDir == "" {
		c.Storage.LocalPreviewDir = "./data/previews"
	}
	if c.Scanner.MaxDepth == 0 {
		c.Scanner.MaxDepth = 5
	}
	if len(c.Scanner.VideoExtensions) == 0 {
		c.Scanner.VideoExtensions = []string{".mp4", ".mkv", ".mov", ".webm", ".avi"}
	}
	if c.Preview.FFmpegPath == "" {
		c.Preview.FFmpegPath = "ffmpeg"
	}
	if c.Preview.FFprobePath == "" {
		c.Preview.FFprobePath = "ffprobe"
	}
	if c.Preview.DurationSeconds != 3 {
		c.Preview.DurationSeconds = 3
	}
	if c.Preview.Width == 0 {
		c.Preview.Width = 480
	}
	if c.Preview.Segments == 0 {
		c.Preview.Segments = 3
	}
}
