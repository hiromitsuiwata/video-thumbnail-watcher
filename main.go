package main

import (
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
)

const FFPROBE = "ffprobe"
const MOV = "download"
const GIF = "img"

func main() {
	inputDir, outputDir := parseArguments()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go watch(*watcher, outputDir)

	err = watcher.Add(inputDir)
	log.Println("watching...")
	if err != nil {
		log.Fatal(err)
	}
	<-done
}

func watch(watcher fsnotify.Watcher, outputDir string) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				log.Printf("create: %s\n", event.Name)
				handleFile(event.Name, outputDir)
			}
			if event.Op&fsnotify.Rename == fsnotify.Rename {
				log.Printf("rename: %s\n", event.Name)
				handleFile(event.Name, outputDir)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("error:", err)
		}
	}
}

func handleFile(inputFile string, outputDir string) {
	// ディレクトリ、ファイル名、拡張子に分解する
	dir, filename, ext := splitFilePath(inputFile)

	// 動画ファイル以外はスキップ
	if !(ext == ".mp4" || ext == ".wmv") {
		return
	}
	// バックアップファイルはスキップ
	if strings.Contains(filename, "_bak") {
		return
	}

	log.Printf("\x1b[32mhandle: %s\x1b[0m\n", inputFile)

	// ファイルの内容からmd5を作る
	md5, err := getMd5(dir, filename, ext)
	if err != nil {
		log.Printf("\x1b[31mmd5 error: %s\x1b[0m\n", filename+ext)
		return
	}

	// md5をもとに出力先ディレクトリに同名ファイルが存在する場合はスキップ
	outputProbe := filepath.Join(outputDir, FFPROBE, md5+".txt")
	_, err = os.Stat(outputProbe)
	if err == nil {
		log.Printf("\x1b[31mfile exists: %s\x1b[0m\n", outputProbe)
		return
	}

	newFilePath := filepath.Join(dir, md5+ext)
	if inputFile == newFilePath {
		// リネーム後のファイル名が同じになる場合はスキップ
		log.Printf("\x1b[31msame file: %s\x1b[0m\n", filename+ext)
		return
	} else {
		// リネームを実行
		log.Printf("rename: %s -> %s\n", inputFile, newFilePath)
		err = os.Rename(inputFile, newFilePath)
		if err != nil {
			log.Printf("\x1b[31mrename error\x1b[0m\n")
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
	log.Printf("thumbnail created: %s/%s%s\n", dir, md5, ".gif")

	// 中間ファイルを削除する
	clean(dir, md5, ext)
	log.Printf("clean temp files\n")

	// 出力先にファイルをコピーする
	log.Printf("copy files start...\n")
	copy(filepath.Join(dir, md5+".txt"), filepath.Join(outputDir, FFPROBE, md5+".txt"))
	copy(filepath.Join(dir, md5+".gif"), filepath.Join(outputDir, GIF, md5+".gif"))
	copy(filepath.Join(dir, md5+ext), filepath.Join(outputDir, MOV, md5+ext))
	log.Printf("copy files end\n")

	log.Println("md5: ", md5)
}

func copy(src string, dst string) {
	dstFile, err := os.Create(dst)
	if err != nil {
		log.Println("cannot crete dst. error:", err)
		return
	} else {
		log.Printf("create dst: %s", dst)
	}
	defer dstFile.Close()

	srcFile, err := os.Open(src)
	if err != nil {
		log.Println("cannot crete src. error:", err)
	} else {
		log.Printf("create src: %s", src)
	}
	defer srcFile.Close()

	log.Printf("copy start: %s -> %s\n", src, dst)
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		log.Println("copy error: ", err)
	} else {
		log.Printf("copy end: %s -> %s\n", src, dst)
	}
}

// ファイルパスをディレクトリ、ファイルベース名、拡張子に分解する
// 拡張子は先頭にピリオドを含む
func splitFilePath(file string) (string, string, string) {
	base := filepath.Base(file)
	ext := filepath.Ext(file)
	dir := filepath.Dir(file)
	filename := strings.TrimSuffix(base, ext)
	log.Printf("dir: %s, filename: %s, ext: %s\n", dir, filename, ext)
	return dir, filename, ext
}

func getMd5(dir string, filename string, ext string) (string, error) {
	filePath := filepath.Join(dir, filename+ext)
	file, err := os.Open(filePath)
	if err != nil {
		log.Println("md5生成エラー os.Open: ", err)
		return "", err
	}
	defer file.Close()

	hash := md5.New()

	if _, err := io.Copy(hash, file); err != nil {
		log.Println("md5生成エラー io.Copy", err)
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
	err := os.WriteFile(probeFilePath, out, 0644)
	if err != nil {
		log.Println(err)
		log.Fatal("probe error")
	}
}

func createSceneCSV(dir string, filename string, ext string) {
	filePath := filepath.Join(dir, filename+ext)
	log.Printf("createSceneCSV: %s\n", filePath)
	_, err := exec.Command("scenedetect", "--input", filePath, "-o", dir, "detect-content", "list-scenes").CombinedOutput()
	if err != nil {
		log.Println(err)
		log.Fatal("scenedetect error")
	}
}

func readCSV(dir string, filename string, ext string) []float32 {
	filePath := filepath.Join(dir, filename+ext)
	log.Printf("readCSV filePath: %s\n", filePath)
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
		cmd := exec.Command("ffmpeg", "-y", "-ss", s, "-i", filePath, "-vframes", "1", "-vf", "scale=320:-1", "-f", "image2", imageFilePath)
		cmd.Start()
		cmd.Wait()
	}

	imageFiles := filepath.Join(dir, filename+"*.jpg")

	gifFile := filepath.Join(dir, filename+".gif")
	cmd := exec.Command("C:\\Program Files\\ImageMagick-7.1.0-portable-Q16-x64\\convert.exe", "-delay", "70", imageFiles, gifFile)
	cmd.Start()
	cmd.Wait()
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

func parseArguments() (string, string) {
	// 入力ディレクトリ
	var inputDir string
	flag.StringVar(&inputDir, "i", "", "input directory")

	// 移動先ディレクトリ
	var outputDir string
	flag.StringVar(&outputDir, "o", ".", "output directory")

	// 引数をパース
	flag.Parse()

	// 入力ディレクトリが指定されていない場合は異常終了
	if len(inputDir) == 0 {
		log.Printf("\x1b[31m input directory(-i) is required \x1b[0m\n")
		os.Exit(1)
	}

	log.Println("input directory: ", inputDir)
	log.Println("output directory: ", outputDir)

	// 入力ディレクトリが存在しないかディレクトリでない場合は異常終了
	fileInfo, err := os.Stat(inputDir)
	if os.IsNotExist(err) {
		log.Printf("\x1b[31m %s + is not exist \x1b[0m\n", inputDir)
		os.Exit(1)
	}
	if !fileInfo.IsDir() {
		log.Printf("\x1b[31m%s is not a directory\x1b[0m\n", inputDir)
		os.Exit(1)
	}

	return inputDir, outputDir
}
