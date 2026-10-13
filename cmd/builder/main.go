package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Supported video extensions from Canon R6 Mk II
var validExtensions = map[string]bool{
	".mp4": true,
	".mov": true,
	".MXF": true,
	".mxf": true,
}

func main() {
	watchDir := flag.String("dir", "", "Path to the root folder on the NAS to monitor")
	flag.Parse()

	if *watchDir == "" {
		log.Fatal("Please provide a directory to watch using -dir")
	}

	absPath, err := filepath.Abs(*watchDir)
	if err != nil {
		log.Fatalf("Invalid directory path: %v", err)
	}

	log.Printf("Starting proxy generator watcher on: %s", absPath)

	// 1. Initial scan of existing files to process any backlog
	go func() {
		log.Println("Performing initial scan for existing C-Log files...")
		err := filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && isValidVideo(path) {
				processFile(path)
			}
			return nil
		})
		if err != nil {
			log.Printf("Error scanning existing files: %v", err)
		} else {
			log.Println("Initial scan completed. Listening for new files...")
		}
	}()

	// 2. Set up fsnotify watcher for real-time additions
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create file watcher: %v", err)
	}
	defer watcher.Close()

	var wg sync.WaitGroup

	// Recursively add all subdirectories to the watcher
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip existing Proxy folders to avoid infinite loops
			if info.Name() == "Proxy" {
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to walk and add paths to watcher: %v", err)
	}

	// Event loop
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Handle creation of new files or directories
			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err != nil {
					continue
				}

				if info.IsDir() {
					if filepath.Base(event.Name) != "Proxy" {
						watcher.Add(event.Name)
						log.Printf("New directory added to watch list: %s", event.Name)
					}
				} else if isValidVideo(event.Name) {
					wg.Add(1)
					go func(filePath string) {
						defer wg.Done()
						// Slight delay to ensure the file finished writing/copying over the network
						time.Sleep(2 * time.Second)
						processFile(filePath)
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

func isValidVideo(path string) bool {
	ext := filepath.Ext(path)
	// Ignore files already inside a Proxy folder
	if strings.Contains(path, string(filepath.Separator)+"Proxy"+string(filepath.Separator)) {
		return false
	}
	return validExtensions[ext]
}

func processFile(srcPath string) {
	dir := filepath.Dir(srcPath)
	filename := filepath.Base(srcPath)
	ext := filepath.Ext(filename)
	nameOnly := strings.TrimSuffix(filename, ext)

	// DaVinci Resolve standard proxy folder structure: <SourceDir>/Proxy/<Filename>.mov
	proxyDir := filepath.Join(dir, "Proxy")
	if err := os.MkdirAll(proxyDir, 0755); err != nil {
		log.Printf("Failed to create proxy directory %s: %v", proxyDir, err)
		return
	}

	proxyPath := filepath.Join(proxyDir, nameOnly+".mov")

	// Check if proxy already exists
	if _, err := os.Stat(proxyPath); err == nil {
		// Proxy already exists, skip
		return
	}

	log.Printf("Generating proxy for: %s", filename)

	// FFmpeg command optimized for DaVinci Resolve proxies:
	// - Half resolution (1920x1080 for 4K source)
	// - ProRes 422 Proxy or DNxHR LB (ProRes is native and smooth, use DNxHR if on Windows without ProRes license,
	//   though ffmpeg's internal prores encoder works great).
	cmd := exec.Command("ffmpeg",
		"-i", srcPath,
		"-vf", "scale=iw/2:ih/2", // Downscale 4K C-Log to 1080p proxy
		"-c:v", "prores_ks",
		"-profile:v", "0", // ProRes 0 = ProRes Proxy
		"-c:a", "pcm_s16le", // Uncompressed audio for timeline sync
		"-y",
		proxyPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error generating proxy for %s: %v\nOutput: %s", filename, err, string(output))
		return
	}

	log.Printf("Successfully generated proxy: %s", proxyPath)
}
