package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

var validExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".mxf":  true,
}

func main() {
	watchDir := flag.String("dir", "", "Path to the root folder on the NAS to monitor")
	proxyDir := flag.String("proxy-dir", "", "Path to the writable directory where proxies should be saved")
	flag.Parse()

	if *watchDir == "" || *proxyDir == "" {
		log.Fatal("Please provide both -dir and -proxy-dir flags")
	}

	absWatch, err := filepath.Abs(*watchDir)
	if err != nil {
		log.Fatalf("Invalid watch directory path: %v", err)
	}

	absProxy, err := filepath.Abs(*proxyDir)
	if err != nil {
		log.Fatalf("Invalid proxy directory path: %v", err)
	}

	log.Printf("Watching NAS directory: %s", absWatch)
	log.Printf("Saving proxies to writable directory: %s", absProxy)

	// 1. Initial scan of existing files
	go func() {
		log.Println("Performing initial scan for existing C-Log files...")
		filepath.Walk(absWatch, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && isValidVideo(path, absWatch) {
				processFile(path, absWatch, absProxy)
			}
			return nil
		})
		log.Println("Initial scan completed. Listening for new files...")
	}()

	// 2. Set up fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create file watcher: %v", err)
	}
	defer watcher.Close()

	filepath.Walk(absWatch, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})

	// Event loop
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}

				if info.IsDir() {
					watcher.Add(event.Name)
					log.Printf("New directory added to watch list: %s", event.Name)
				} else if isValidVideo(event.Name, absWatch) {
					go func(filePath string) {
						time.Sleep(2 * time.Second) // Wait for network copy to finish
						processFile(filePath, absWatch, absProxy)
					}(event.Name)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func isValidVideo(path string, rootDir string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return validExtensions[ext]
}

func processFile(srcPath string, rootDir string, proxyRoot string) {
	// Calculate relative path from the root watch directory to preserve folder structure
	relPath, err := filepath.Rel(rootDir, srcPath)
	if err != nil {
		log.Printf("Failed to get relative path for %s: %v", srcPath, err)
		return
	}

	// Change extension to .mov for proxy
	ext := filepath.Ext(relPath)
	relProxyPath := strings.TrimSuffix(relPath, ext) + ".mov"
	
	destPath := filepath.Join(proxyRoot, relProxyPath)
	destDir := filepath.Dir(destPath)

	// Create mirrored subdirectories in the writable proxy location
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Printf("Failed to create proxy directory %s: %v", destDir, err)
		return
	}

	// Check if proxy already exists
	if _, err := os.Stat(destPath); err == nil {
		return
	}

	log.Printf("Generating proxy for: %s", relPath)

	cmd := exec.Command("ffmpeg",
		"-i", srcPath,
		"-vf", "scale=iw/2:ih/2",
		"-c:v", "prores_ks",
		"-profile:v", "0",
		"-c:a", "pcm_s16le",
		"-y",
		destPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error generating proxy for %s: %v\nOutput: %s", relPath, err, string(output))
		return
	}

	log.Printf("Successfully generated proxy: %s", destPath)
}
