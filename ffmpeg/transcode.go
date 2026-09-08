// Package ffmpeg 封装 ffmpeg 命令行调用：命令解析、WebM 转 MP4、
// 以及基于标准库图像绘制的路书总结视频生成，不依赖 CGO。
package ffmpeg

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolve 查找可用的 ffmpeg 命令。
func Resolve(path string) (string, error) {
	if value := strings.TrimSpace(path); value != "" {
		if resolved, err := exec.LookPath(value); err == nil {
			return resolved, nil
		}
		return "", fmt.Errorf("ffmpeg command not found")
	}
	return exec.LookPath("ffmpeg")
}

// TranscodeWebMToMP4 将 WebM 转为 H.264/AAC MP4。
func TranscodeWebMToMP4(ctx context.Context, ffmpegPath string, inputPath string, outputPath string) error {
	args := []string{
		"-y",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map", "0:a?",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "192k",
		"-shortest",
		"-movflags", "+faststart",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg 转码失败: %s", tailText(string(output), 800))
	}
	return nil
}

// RoutePoint 定义服务端导出路线图中的坐标点。
type RoutePoint struct {
	Lat float64
	Lng float64
}

// GenerateRouteBookSummaryMP4 使用路书坐标生成服务端 MP4。
func GenerateRouteBookSummaryMP4(ctx context.Context, ffmpegPath string, outputPath string, points []RoutePoint, durationSeconds int) error {
	if durationSeconds <= 0 {
		durationSeconds = 8
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(outputPath), "routebook-poster-*")
	if err != nil {
		return fmt.Errorf("创建路书导出临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)
	posterPath := filepath.Join(tempDir, "poster.png")
	if err := writeRoutePoster(posterPath, points); err != nil {
		return err
	}
	args := []string{
		"-y",
		"-loop", "1",
		"-i", posterPath,
		"-t", fmt.Sprintf("%d", durationSeconds),
		"-r", "30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg 路书视频生成失败: %s", tailText(string(output), 800))
	}
	return nil
}

// writeRoutePoster 使用 Go 标准库生成路线海报图。
func writeRoutePoster(path string, points []RoutePoint) error {
	const width = 1280
	const height = 720
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{248, 250, 252, 255}}, image.Point{}, draw.Src)
	drawGrid(canvas, width, height)
	screenPoints := normalizeRoutePoints(points, width, height)
	for i := 1; i < len(screenPoints); i++ {
		drawLine(canvas, screenPoints[i-1], screenPoints[i], color.RGBA{15, 159, 134, 255}, 6)
	}
	for index, point := range screenPoints {
		radius := 12
		fill := color.RGBA{15, 159, 134, 255}
		if index == 0 {
			radius = 15
			fill = color.RGBA{37, 99, 235, 255}
		}
		if index == len(screenPoints)-1 {
			radius = 15
			fill = color.RGBA{220, 38, 38, 255}
		}
		drawCircle(canvas, point.X, point.Y, radius+4, color.RGBA{255, 255, 255, 255})
		drawCircle(canvas, point.X, point.Y, radius, fill)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建路书海报失败: %w", err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		return fmt.Errorf("编码路书海报失败: %w", err)
	}
	return nil
}

// drawGrid 绘制轻量地图网格背景。
func drawGrid(canvas *image.RGBA, width int, height int) {
	gridColor := color.RGBA{226, 232, 240, 255}
	for x := 80; x < width; x += 120 {
		drawLine(canvas, image.Point{X: x, Y: 0}, image.Point{X: x, Y: height}, gridColor, 1)
	}
	for y := 80; y < height; y += 100 {
		drawLine(canvas, image.Point{X: 0, Y: y}, image.Point{X: width, Y: y}, gridColor, 1)
	}
}

// normalizeRoutePoints 将经纬度归一化到视频画布坐标。
func normalizeRoutePoints(points []RoutePoint, width int, height int) []image.Point {
	if len(points) == 0 {
		return []image.Point{{X: 180, Y: height / 2}, {X: width - 180, Y: height / 2}}
	}
	minLat, maxLat := points[0].Lat, points[0].Lat
	minLng, maxLng := points[0].Lng, points[0].Lng
	for _, point := range points[1:] {
		minLat = math.Min(minLat, point.Lat)
		maxLat = math.Max(maxLat, point.Lat)
		minLng = math.Min(minLng, point.Lng)
		maxLng = math.Max(maxLng, point.Lng)
	}
	marginX, marginY := 120.0, 100.0
	lngRange := maxLng - minLng
	latRange := maxLat - minLat
	if lngRange == 0 {
		lngRange = 1
	}
	if latRange == 0 {
		latRange = 1
	}
	out := make([]image.Point, 0, len(points))
	for index, point := range points {
		x := marginX + ((point.Lng-minLng)/lngRange)*float64(width-int(marginX*2))
		y := marginY + ((maxLat-point.Lat)/latRange)*float64(height-int(marginY*2))
		if len(points) == 1 {
			x = float64(width / 2)
			y = float64(height / 2)
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
			x = marginX + float64(index)*80
			y = float64(height / 2)
		}
		out = append(out, image.Point{X: int(math.Round(x)), Y: int(math.Round(y))})
	}
	return out
}

// drawLine 绘制指定粗细的线段。
func drawLine(canvas *image.RGBA, start image.Point, end image.Point, c color.RGBA, thickness int) {
	dx := end.X - start.X
	dy := end.Y - start.Y
	steps := int(math.Max(math.Abs(float64(dx)), math.Abs(float64(dy))))
	if steps == 0 {
		drawCircle(canvas, start.X, start.Y, thickness, c)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(math.Round(float64(start.X) + float64(dx)*t))
		y := int(math.Round(float64(start.Y) + float64(dy)*t))
		drawCircle(canvas, x, y, thickness, c)
	}
}

// drawCircle 绘制实心圆。
func drawCircle(canvas *image.RGBA, cx int, cy int, radius int, c color.RGBA) {
	bounds := canvas.Bounds()
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius {
				canvas.SetRGBA(x, y, c)
			}
		}
	}
}

// tailText 返回指定长度以内的末尾文本。
func tailText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}
