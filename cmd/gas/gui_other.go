//go:build !windows

package main

import (
	"context"
	"log"
)

// 非 Windows（開發用）：用瀏覽器開。
type ui struct {
	url    string
	hidden bool
}

func newUI(url, dir string) *ui { return &ui{url: url} }

func (u *ui) Run(ctx context.Context) {
	if !u.hidden {
		openBrowser(u.url)
	}
	<-ctx.Done()
}
func (u *ui) Show()             { openBrowser(u.url) }
func (u *ui) Quit()             {}
func (u *ui) Notify(msg string) { log.Println(msg) }

func showError(title, msg string) { log.Println(title, msg) }
