package livesplit_race_manager

import (
	"embed"
	"fmt"
	"github.com/Masterminds/sprig/v3"
	"github.com/gin-contrib/logger"
	"github.com/gin-gonic/gin"
	"github.com/golang/protobuf/ptypes/duration"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/slices"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

var (
	//go:embed html/*.gohtml
	htmlTemplates embed.FS
)

type NewAPIOpts struct {
	LSM     *LiveSplitManager
	Address string
}

func NewAPI(opts NewAPIOpts) error {
	if opts.LSM == nil {
		return errors.New("LSM can't be nil")
	}
	if opts.Address == "" {
		return errors.New("isten address can't be empty")
	}

	SetGinLogMode()
	r := gin.New()
	r.Use(logger.SetLogger(), gin.Recovery())

	t, err := loadTemplates()
	if err != nil {
		panic(err)
	}
	r.SetHTMLTemplate(t)

	r.GET("/", func(c *gin.Context) {
		liveSplits := opts.LSM.LiveSplits()
		slices.SortFunc(liveSplits, func(i, j *LiveSplit) bool {
			return strings.ToLower(i.ID) < strings.ToLower(j.ID)
		})

		c.HTML(http.StatusOK, "html/status.gohtml", liveSplits)
	})
	r.GET("/api/v1/livesplits", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"livesplits": opts.LSM.LiveSplits(),
		})
	})

	log.Info().Str("listen", opts.Address).Msg("Start serving HTTP API")
	err = r.Run(opts.Address)
	if err != nil {
		return err
	}
	return nil
}

func loadTemplates() (*template.Template, error) {
	t := template.New("").Funcs(sprig.FuncMap()).Funcs(template.FuncMap{
		"durationToString": func(duration *duration.Duration) string {
			if duration == nil {
				return "-"
			}
			hours := duration.Seconds / 3600
			minutes := (duration.Seconds / 60) % 60
			seconds := duration.Seconds % 60
			ms := duration.Nanos / 1000000
			return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, ms)
		},
	})

	err := fs.WalkDir(htmlTemplates, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		h, err := fs.ReadFile(htmlTemplates, path)
		if err != nil {
			return err
		}
		t, err = t.New(path).Parse(string(h))
		if err != nil {
			return err
		}

		return nil
	})

	return t, err
}

func SetGinLogMode() {
	var mode string
	switch zerolog.GlobalLevel() {
	case zerolog.DebugLevel:
		mode = gin.DebugMode
	default:
		mode = gin.ReleaseMode
	}
	gin.SetMode(mode)
}
