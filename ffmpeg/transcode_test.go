package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateRouteBookSummaryMP4 验证服务端路书导出不依赖 drawtext 也能生成 MP4。
func TestGenerateRouteBookSummaryMP4(t *testing.T) {
	ffmpegPath, err := Resolve("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	outputPath := filepath.Join(t.TempDir(), "routebook.mp4")
	err = GenerateRouteBookSummaryMP4(context.Background(), ffmpegPath, outputPath, []RoutePoint{
		{Lat: 30.57, Lng: 104.06},
		{Lat: 30.05, Lng: 101.96},
		{Lat: 29.98, Lng: 102.23},
	}, 1)
	if err != nil {
		t.Fatalf("generate route book mp4 failed: %v", err)
	}
	stat, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat output failed: %v", err)
	}
	if stat.Size() == 0 {
		t.Fatal("output mp4 is empty")
	}
}
