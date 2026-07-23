package main

import (
	"embed"
	"log"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"nice_codex_desktop/internal/codex"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[codex.Event]("codex:event")
}

func main() {
	windowsOpts := application.WindowsOptions{}
	if port := strings.TrimSpace(os.Getenv("NICE_CODEX_CDP_PORT")); port != "" {
		windowsOpts.AdditionalBrowserArgs = []string{
			"--remote-debugging-port=" + port,
			"--remote-allow-origins=*",
		}
	}

	app := application.New(application.Options{
		Name:        "Nice Codex",
		Description: "A lightweight Codex-compatible desktop client.",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Windows: windowsOpts,
	})

	service := NewAppService(app)
	app.RegisterService(application.NewService(service))
	app.OnShutdown(service.shutdown)

	// Frameless + hidden native caption buttons: custom HTML TitleBar owns chrome.
	// See https://v3.wails.io/features/windows/frameless/
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Nice Codex",
		Width:            1380,
		Height:           860,
		MinWidth:         980,
		MinHeight:        680,
		Frameless:        true,
		BackgroundColour: application.NewRGB(243, 243, 243),
		URL:              "/",
		EnableFileDrop:   true,
		// Hide residual native caption controls if DWM still paints them.
		MinimiseButtonState: application.ButtonHidden,
		MaximiseButtonState: application.ButtonHidden,
		CloseButtonState:    application.ButtonHidden,
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTranslucent,
			TitleBar: application.MacTitleBarHidden,
		},
		Windows: application.WindowsWindow{
			DisableIcon: true,
			// Keep Aero shadow / Win11 rounded corners while Frameless removes the caption.
			DisableFramelessWindowDecorations: false,
		},
	})
	// Reinforce after construction (docs also allow runtime SetFrameless).
	window.SetFrameless(true)
	window.SetMinimiseButtonState(application.ButtonHidden)
	window.SetMaximiseButtonState(application.ButtonHidden)
	window.SetCloseButtonState(application.ButtonHidden)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
