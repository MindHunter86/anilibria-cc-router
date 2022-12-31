package main

import (
	"errors"
	"hash/maphash"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/urfave/cli/v2"
	"github.com/valyala/fasthttp"
)

var version = "devel" // -ldflags="-X 'main.version=X.X.X'"

var log zerolog.Logger

func main() {
	go func() {
		log.Print(http.ListenAndServe("localhost:6068", nil))
	}()

	// logger
	log = zerolog.New(zerolog.ConsoleWriter{
		Out: os.Stderr,
	}).With().Timestamp().Logger().Hook(SeverityHook{})
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	// application
	app := cli.NewApp()
	cli.VersionFlag = &cli.BoolFlag{Name: "version", Aliases: []string{"V"}}

	app.Name = "anilibria-cc-router"
	app.Version = version
	app.Compiled = time.Now()
	app.Authors = []*cli.Author{
		&cli.Author{
			Name:  "MindHunter86",
			Email: "mindhunter86@vkom.cc",
		},
	}
	app.Copyright = "(c) 2022-2023 mindhunter86\nwith love for Anilibria project"
	app.Usage = "Cloud Cache Router for Anilibria project"

	app.Flags = []cli.Flag{
		// common flags
		&cli.StringFlag{
			Name:    "log-level",
			Aliases: []string{"l"},
			Value:   "debug",
			Usage:   "levels: trace, debug, info, warn, err, panic, disabled",
			EnvVars: []string{"LOG_LEVEL"},
		},
		&cli.BoolFlag{
			Name:    "quite",
			Aliases: []string{"q"},
			Usage:   "Flag is equivalent to --log-level=quite",
		},

		// fasthttp settings
		&cli.StringFlag{
			Name:  "listen-addr",
			Value: ":8089",
		},
	}

	app.Action = func(c *cli.Context) (e error) {
		var lvl zerolog.Level
		if lvl, e = zerolog.ParseLevel(c.String("log-level")); e != nil {
			log.Fatal().Err(e)
		}

		zerolog.SetGlobalLevel(lvl)
		if c.Bool("quite") {
			zerolog.SetGlobalLevel(zerolog.Disabled)
		}

		log.Debug().Msg("ready...")
		log.Debug().Strs("args", os.Args).Msg("")

		return newService(c).bootstrap()
	}

	// TODO sort.Sort of Flags uses too much allocs; temporary disabled
	// sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))

	if e := app.Run(os.Args); e != nil {
		log.Fatal().Err(e).Msg("")
	}
}

type SeverityHook struct{}

func (SeverityHook) Run(e *zerolog.Event, level zerolog.Level, _ string) {
	if level > zerolog.DebugLevel || version != "devel" {
		return
	}

	rfn := "unknown"
	pcs := make([]uintptr, 1)

	if runtime.Callers(4, pcs) != 0 {
		if fun := runtime.FuncForPC(pcs[0] - 1); fun != nil {
			rfn = fun.Name()
		}
	}

	fn := strings.Split(rfn, "/")
	e.Str("func", fn[len(fn)-1:][0])
}

var (
	errApiHeadersUndefined = errors.New("coudl not parse some required headers")
)

type service struct {
	locker  sync.RWMutex
	storage map[uint64][]byte

	seed maphash.Seed

	c *cli.Context
}

func newService(c *cli.Context) *service {
	return &service{
		storage: make(map[uint64][]byte),
		seed:    maphash.MakeSeed(),

		c: c,
	}
}

func (m *service) bootstrap() (e error) {
	return fasthttp.ListenAndServe(m.c.String("listen-addr"), m.httpHandler)
}

func (*service) hlpRespondError(r *fasthttp.Response, err error, status ...int) {
	status = append(status, fasthttp.StatusInternalServerError)

	r.Header.Set("X-Error", err.Error())
	r.SetStatusCode(status[0])

	log.Error().Err(err).Msg("")
}

func (m *service) httpHandler(ctx *fasthttp.RequestCtx) {
	if len(ctx.Request.Header.Peek("X-Cache-Server")) == 0 {
		m.hlpRespondError(&ctx.Response, errApiHeadersUndefined, fasthttp.StatusBadRequest)
		return
	}

	cserver, ok := m.getCacheNode(ctx.Request.URI().Path())
	if !ok {
		log.Trace().Msg("could not found cache server, trying to write new element in storage...")
		cserver, ok = m.pushCacheNode(ctx.Request.URI().Path(), ctx.Request.Header.Peek("X-Cache-Server"))
		if !ok {
			log.Trace().Msg("there is no new element was writed; it seems that requested server appears beetween locks")
		} else {
			log.Trace().Msg("new element has been added in storage")
		}
	} else {
		log.Trace().Msg("cache node has been found in storage")
	}

	ctx.Response.Header.SetBytesV("X-Requested-Server", ctx.Request.Header.Peek("X-Cache-Server"))
	ctx.Response.Header.SetBytesV("X-Location", cserver)
	ctx.Response.SetStatusCode(fasthttp.StatusOK)
}

// TODO optimize, remove allocations
func (m *service) getMapKeyFromUri(uri []byte) uint64 {
	var hsh maphash.Hash
	hsh.SetSeed(m.seed)

	hsh.Write(uri)
	return hsh.Sum64()
}

func (m *service) getCacheNode(uri []byte) (val []byte, ok bool) {
	sum := m.getMapKeyFromUri(uri)
	log.Trace().Uint64("value", sum).Msg("")

	m.locker.RLock()

	val, ok = m.storage[sum]
	m.locker.RUnlock()

	log.Trace().Bool("ok", ok).Str("val", string(val)).Msg("")

	return
}

// TODO optimize lockers
func (m *service) pushCacheNode(uri []byte, server []byte) (val []byte, ok bool) {
	sum := m.getMapKeyFromUri(uri)
	m.locker.Lock()

	log.Trace().Msg("trying to push new value")

	val, ok = m.storage[sum]

	if ok {
		log.Trace().Msg("returned value is ok, stop pushing")
		m.locker.Unlock()
		return val, false
	}

	value := make([]byte, len(server))
	log.Print(copy(value, server))
	m.storage[sum] = value
	m.locker.Unlock()

	log.Trace().Str("server", string(value)).Msg("pushed new value")

	return value, true
}
