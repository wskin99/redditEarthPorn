package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SlyMarbo/rss"
	query "github.com/hailiang/html-query"
	expr "github.com/hailiang/html-query/expr"
)

func main() {
	quit := make(chan os.Signal)
	signal.Notify(quit, os.Interrupt, os.Kill)

	feed, err := rss.Fetch("http://reddit.com/r/earthporn.rss")
	if err != nil {
		log.Fatal(err)
	}

	run(feed)

daLoop:
	for {
		select {
		case <-time.After(1 * time.Hour):
			feed.Update()
			run(feed)
		case <-quit:
			break daLoop
		}
	}
}

func run(feed *rss.Feed) {
	log.Printf("Starting run at %s\n", time.Now())
	for _, item := range feed.Items {
		link, title, err := extractInfo(item)
		if err != nil {
			log.Printf("Couldn't parse html for %s: %s\n", item.Link, err)
			continue
		}

		err = store(link, title)
		if err != nil {
			log.Printf("Couldn't store [%s]: %s\n", title, err)
		}
	}

	var totalSize int64
	entries := make([]os.FileInfo, 0)
	filepath.Walk("shared", func(path string, info os.FileInfo, perr error) error {
		totalSize += info.Size()
		entries = append(entries, info)
		return nil
	})

	log.Printf("Starting cleanup, totalSize is %d", totalSize)

	if totalSize >= 1<<26 { // ~67 MiB
		sort.Sort(byAgeDesc(entries))
		var currentSize int64
		for _, info := range entries {
			if currentSize >= 1<<26 {
				err := os.Remove(filepath.Join("shared", info.Name()))
				if err != nil {
					log.Printf("Couldn't remove [%s]: %s\n", info.Name(), err)
					continue
				}
			}
			currentSize += info.Size()
		}
	}

	log.Printf("Finished run at %s\n", time.Now())
}

type byAgeDesc []os.FileInfo

func (b byAgeDesc) Len() int      { return len(b) }
func (b byAgeDesc) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byAgeDesc) Less(i, j int) bool {
	return b[i].ModTime().After(b[i].ModTime())
}

func extractInfo(item *rss.Item) (link, title string, err error) {
	root, err := query.Parse(strings.NewReader(item.Content))
	if err != nil {
		return
	}

	//fmt.Printf("title: %s\n", *root.Img().Attr("title"))
	root.Table().Tbody().Tr().Children().For(func(td *query.Node) {
		img := td.Ahref().Img()
		if img != nil {
			title = *img.Attr("title")
		}

		td.Children(expr.Ahref).For(func(ahref *query.Node) {
			//fmt.Printf("%#v\n", ahref.Children().All()[0])
			if ahref.Text() != nil && *ahref.Text() == "[link]" {
				link = *ahref.Href()
				url, err := url.Parse(link)
				if err != nil {
					return
				}
				if url.Host == "imgur.com" {
					link = link + ".jpg"
				}
			}
		})
	})

	return
}

func store(link, title string) error {

	img, err := http.Get(link)
	if err != nil {
		return err
	}
	defer img.Body.Close()

	var ctHeader bytes.Buffer
	_, err = io.CopyN(&ctHeader, img.Body, 512)
	if err != nil {
		return err
	}
	typ := http.DetectContentType(ctHeader.Bytes())
	var ext string
	if strings.Contains(typ, "jpg") || strings.Contains(typ, "jpeg") {
		ext = "jpg"
	} else if strings.Contains(typ, "png") {
		ext = "png"
	} else {
		log.Printf("Unknown image type (%s)\n", typ)
		return errors.New(fmt.Sprintf("No extension for %s", link))
	}

	f, err := os.Create(path.Join("shared", title+"."+ext))
	if err != nil {
		return err
	}
	defer f.Close()

	mr := io.MultiReader(&ctHeader, img.Body)
	_, err = io.Copy(f, mr)
	return nil
}
