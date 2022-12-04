package main

import (
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
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
				fmt.Printf("create: %s\n", event.Name)
				handleFile(event.Name)
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				fmt.Printf("rename: %s\n", event.Name)
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

func handleFile(input_filepath string) {
	// ディレクトリ、ファイル名、拡張子に分解する
	dir, filename, ext := splitFilePath(input_filepath)

	// 動画ファイル以外はスキップ
	if !(ext == ".mp4" || ext == ".wmv") {
		return
	}
	// バックアップファイルはスキップ
	if strings.Contains(filename, "_bak") {
		return
	}

	fmt.Printf("\x1b[32mhandle: %s\x1b[0m\n", input_filepath)

	// ファイルの内容からmd5を作る
	md5, err := getMd5(dir, filename, ext)
	if err != nil {
		fmt.Printf("\x1b[31mmd5 error: %s\x1b[0m\n", filename+ext)
		return
	}

	newFilePath := filepath.Join(dir, md5+ext)
	if input_filepath == newFilePath {
		// リネーム後のファイル名が同じになる場合はスキップ
		fmt.Printf("\x1b[31msame file: %s\x1b[0m\n", filename+ext)
		return
	} else {
		// リネームを実行
		fmt.Printf("rename: %s -> %s", input_filepath, newFilePath)
		err = os.Rename(input_filepath, newFilePath)
		if err != nil {
			fmt.Printf("\x1b[31mrename error\x1b[0m\n")
			return
		}
	}

	// probeファイルを作る
	createFfprobe(dir, md5, ext)

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

// ファイルパスをディレクトリ、ファイルベース名、拡張子に分解する
// 拡張子は先頭にピリオドを含む
func splitFilePath(file string) (string, string, string) {
	base := filepath.Base(file)
	ext := filepath.Ext(file)
	dir := filepath.Dir(file)
	filename := strings.TrimSuffix(base, ext)
	fmt.Printf("dir: %s, filename: %s, ext: %s\n", dir, filename, ext)
	return dir, filename, ext
}

func getMd5(dir string, filename string, ext string) (string, error) {
	filePath := filepath.Join(dir, filename+ext)
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("md5生成エラー os.Open: ", err)
		return "", err
	}
	defer file.Close()

	hash := md5.New()

	if _, err := io.Copy(hash, file); err != nil {
		fmt.Println("md5生成エラー io.Copy", err)
		return "", err
	}

	hashInBytes := hash.Sum(nil)[:16]
	hashString := hex.EncodeToString(hashInBytes)

	return hashString, nil
}

func createFfprobe(dir string, filename string, ext string) {
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
		fmt.Printf("\x1b[31margument path(-p) is required\x1b[0m\n")
		os.Exit(1)
	}
	fileInfo, err := os.Stat(path)
	if os.IsNotExist(err) {
		fmt.Printf("\x1b[31m%s + is not exist\x1b[0m\n", path)
		os.Exit(1)
	}
	if !fileInfo.IsDir() {
		fmt.Printf("\x1b[31m%s is not a directory\x1b[0m\n", path)
		os.Exit(1)
	}
	return path
}
