package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"nice_codex_desktop/internal/codex"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[codex.Event]("codex:event")
}

func main() {
	app := application.New(application.Options{
		Name:        "Nice Codex",
		Description: "A lightweight Codex-compatible desktop client.",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	service := NewAppService(app)
	app.RegisterService(application.NewService(service))
	app.OnShutdown(service.shutdown)

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Nice Codex",
		Width:            1380,
		Height:           860,
		MinWidth:         980,
		MinHeight:        680,
		BackgroundColour: application.NewRGB(20, 21, 18),
		URL:              "/",
		EnableFileDrop:   true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
