package main

import (
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func main() {

	path := parseArgument()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go watch(*watcher)

	err = watcher.Add(path)
	fmt.Println("watching...")
	if err != nil {
		log.Fatal(err)
	}
	<-done
}

func watch(watcher fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				printGreen("create: " + event.Name)
				handleFile(event.Name)
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				printGreen("rename: " + event.Name)
				handleFile(event.Name)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("error:", err)
		}
	}
}

func handleFile(file string) {
	if (strings.HasSuffix(file, ".wmv") || strings.HasSuffix(file, ".mp4")) && !strings.Contains(file, "_bak") {
		printGreen("handle: " + file)
		mainProcess(file)
	}
}

func mainProcess(filePath string) {
	// ディレクトリ、ファイル名、拡張子に分解する
	dir, filename, ext := splitFilePath(filePath)

	// ファイルの内容からmd5を作る
	md5, err := renameToMD5(dir, filename, ext)
	if err != nil {
		fmt.Println("renameToMD5 error")
		return
	}
	log.Printf("rename to md5: %s", md5)

	// probeファイルを作る
	createFFPROBE(dir, md5, ext)

	// シーン情報を取得する
	createSceneCSV(dir, md5, ext)

	// シーン情報から静止画を切り出す
	scenes := readCSV(dir, md5+"-Scenes", ".csv")

	// 静止画からサムネイルGIFを作る
	createThumbnailGif(dir, md5, ext, scenes)

	// 中間ファイルを削除する
	clean(dir, md5, ext)

	log.Printf("thumbnail created!: %s/%s%s\n", dir, md5, ".gif")
	fmt.Println(md5)
}

func splitFilePath(file string) (string, string, string) {
	base := filepath.Base(file)
	ext := filepath.Ext(file)
	dir := filepath.Dir(file)
	filename := strings.TrimSuffix(base, ext)
	log.Printf("dir: %s, filename: %s, ext: %s", dir, filename, ext)
	return dir, filename, ext
}

func renameToMD5(dir string, filename string, ext string) (string, error) {
	fmt.Println("renameToMD5")
	filePath := filepath.Join(dir, filename+ext)
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("md5生成エラー os.Open: ", err)
		return "", err
	}

	hash := md5.New()

	if _, err := io.Copy(hash, file); err != nil {
		fmt.Println("md5生成エラー io.Copy", err)
		return "", err
	}

	file.Close()

	hashInBytes := hash.Sum(nil)[:16]
	hashString := hex.EncodeToString(hashInBytes)

	newFilePath := filepath.Join(dir, hashString+ext)
	log.Println("old path: " + filePath)
	log.Println("new path: " + newFilePath)

	if filePath == newFilePath {
		return hashString, errors.New("same file")
	}

	err = os.Rename(filePath, newFilePath)
	if err != nil {
		log.Fatal("rename error")
	}

	return hashString, nil
}

func createFFPROBE(dir string, filename string, ext string) {
	filePath := filepath.Join(dir, filename+ext)
	out, _ := exec.Command("ffprobe", filePath).CombinedOutput()

	probeFilePath := filepath.Join(dir, filename+".txt")
	err := ioutil.WriteFile(probeFilePath, out, 0644)
	if err != nil {
		log.Println(err)
		log.Fatal("probe error")
	}
}

func createSceneCSV(dir string, filename string, ext string) {
	filePath := filepath.Join(dir, filename+ext)
	log.Println("createSceneCSV: " + filePath)
	_, err := exec.Command("scenedetect", "--input", filePath, "-o", dir, "detect-content", "list-scenes").CombinedOutput()
	if err != nil {
		log.Println(err)
		log.Fatal("scenedetect error")
	}
}

func readCSV(dir string, filename string, ext string) []float32 {
	filePath := filepath.Join(dir, filename+ext)
	log.Println(filePath)
	reader, _ := os.Open(filePath)
	defer reader.Close()
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1

	records, err := csvReader.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	slice := make([]float32, 1)
	for i := 2; i < len(records); i++ {
		a, _ := strconv.ParseFloat(records[i][3], 32)
		b, _ := strconv.ParseFloat(records[i][6], 32)
		mean := float32((a + b) / 2.0)
		// 最後のシーンから10秒以上離れている場合のみ追加する
		if slice[len(slice)-1]+10.0 < mean {
			slice = append(slice, mean)
		}
	}

	return slice
}

func createThumbnailGif(dir string, filename string, ext string, scenes []float32) {
	filePath := filepath.Join(dir, filename+ext)
	for i, v := range scenes {
		s := fmt.Sprintf("%08.3f", v)
		log.Printf("#%3d: %s\n", i, s)
		imageFilePath := filepath.Join(dir, filename+"_"+s+".jpg")
		_, err := exec.Command("ffmpeg", "-y", "-ss", s, "-i", filePath, "-vframes", "1", "-vf", "scale=320:-1", "-f", "image2", imageFilePath).CombinedOutput()
		if err != nil {
			log.Println(err)
			log.Fatal("ffmpeg error")
		}
	}

	imageFiles := filepath.Join(dir, filename+"*.jpg")

	gifFile := filepath.Join(dir, filename+".gif")
	_, err := exec.Command("C:\\Program Files\\ImageMagick-7.1.0-portable-Q16-x64\\convert.exe", "-delay", "70", imageFiles, gifFile).CombinedOutput()
	if err != nil {
		log.Println(err)
		log.Fatal("covert error")
	}
}

func clean(dir string, filename string, ext string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if strings.Contains(path, filename) && (strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".csv")) {
			log.Println(path)
			err2 := os.Remove(path)
			if err2 != nil {
				log.Fatal("remove error")
			}
		}
		return nil
	})
}

func parseArgument() string {
	var path string
	flag.StringVar(&path, "p", "", "a directory to watch")
	flag.Parse()
	fmt.Println("path:", path)
	if len(path) == 0 {
		printRed("argument path(-p) is required")
		os.Exit(1)
	}
	fileInfo, err := os.Stat(path)
	if os.IsNotExist(err) {
		printRed(path + "is not exist")
		os.Exit(1)
	}
	if !fileInfo.IsDir() {
		printRed(path + "is not a directory")
		os.Exit(1)
	}
	return path
}

func printRed(str string) {
	fmt.Printf("\x1b[31m%s\x1b[0m\n", str)
}

func printGreen(str string) {
	fmt.Printf("\x1b[32m%s\x1b[0m\n", str)
}
