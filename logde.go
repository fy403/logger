package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/getsentry/sentry-go"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v2"
)

const (
	TimeDivision = "time"
	SizeDivision = "size"

	_defaultEncoding = "console"
	_defaultDivision = "size"
	_defaultUnit     = Hour
)

var (
	_encoderNameToConstructor = map[string]func(zapcore.EncoderConfig) zapcore.Encoder{
		"console": func(encoderConfig zapcore.EncoderConfig) zapcore.Encoder {
			return zapcore.NewConsoleEncoder(encoderConfig)
		},
		"json": func(encoderConfig zapcore.EncoderConfig) zapcore.Encoder {
			return zapcore.NewJSONEncoder(encoderConfig)
		},
	}
)

type Log struct {
	L *zap.Logger
}

type LogOptions struct {
	// Encoding sets the logger's encoding. Valid values are "json" and
	// "console", as well as any third-party encodings registered via
	// RegisterEncoder.
	Encoding      string             `json:"encoding,omitempty" yaml:"encoding,omitempty" toml:"encoding,omitempty"`
	InfoFilename  string             `json:"info_filename" yaml:"info_filename" toml:"info_filename"`
	ErrorFilename string             `json:"error_filename" yaml:"error_filename" toml:"error_filename"`
	MaxSize       int                `json:"max_size" yaml:"max_size" toml:"max_size"`
	MaxBackups    int                `json:"max_backups" yaml:"max_backups" toml:"max_backups"`
	MaxAge        int                `json:"max_age" yaml:"max_age" toml:"max_age"`
	Compress      bool               `json:"compress" yaml:"compress" toml:"compress"`
	Division      string             `json:"division" yaml:"division" toml:"division"`
	LevelSeparate bool               `json:"level_separate" yaml:"level_separate" toml:"level_separate"`
	TimeUnit      TimeUnit           `json:"time_unit" yaml:"time_unit" toml:"time_unit"`
	Stacktrace    bool               `json:"stacktrace" yaml:"stacktrace" toml:"stacktrace"`
	SentryConfig  SentryLoggerConfig `json:"sentry_config" yaml:"sentry_config" toml:"sentry_config"`
	Level         int8               `json:"level" yaml:"level" toml:"level"`
	CloseDisplay  int                `json:"close_display" yaml:"close_display" toml:"close_display"`
	caller        bool
	skip          int
}

func infoLevel(level int8) zap.LevelEnablerFunc {
	return zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.Level(level) && lvl < zapcore.WarnLevel
	})
}

func warnLevel() zap.LevelEnablerFunc {
	return zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.WarnLevel
	})
}

func logLevel(level int8) zap.LevelEnablerFunc {
	return zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.Level(level)
	})
}

func New() *LogOptions {
	return &LogOptions{
		Division:      _defaultDivision,
		LevelSeparate: false,
		TimeUnit:      _defaultUnit,
		Encoding:      _defaultEncoding,
		caller:        false,
	}
}

func NewFromToml(confPath string) *LogOptions {
	var c *LogOptions
	if _, err := toml.DecodeFile(confPath, &c); err != nil {
		panic(err)
	}
	return c
}

func NewFromYaml(confPath string) *LogOptions {
	var c *LogOptions
	file, err := ioutil.ReadFile(confPath)
	if err != nil {
		fmt.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(file, &c)
	if err != nil {
		fmt.Printf("error: %v", err)
	}
	return c
}

func NewFromJson(confPath string) *LogOptions {
	var c *LogOptions
	file, err := ioutil.ReadFile(confPath)
	if err != nil {
		fmt.Printf("yamlFile.Get err   #%v ", err)
	}
	err = json.Unmarshal(file, &c)
	if err != nil {
		fmt.Printf("error: %v", err)
	}
	return c
}

func (c *LogOptions) SetDivision(division string) {
	c.Division = division
}

func (c *LogOptions) CloseConsoleDisplay() {
	c.CloseDisplay = 1
}

func (c *LogOptions) SetCaller(enable bool, skip int) {
	c.caller = enable
	c.skip = skip
}

func (c *LogOptions) SetTimeUnit(t TimeUnit) {
	c.TimeUnit = t
}

func (c *LogOptions) SetErrorFile(path string) {
	c.LevelSeparate = true
	c.ErrorFilename = path
}

func (c *LogOptions) SetInfoFile(path string) {
	c.InfoFilename = path
}

func (c *LogOptions) SetEncoding(encoding string) {
	c.Encoding = encoding
}

// isOutput whether set output file
func (c *LogOptions) isOutput() bool {
	return c.InfoFilename != ""
}

func (c *LogOptions) InitLogger(timeKey, levelKey string, customEncodeTime, shortCaller bool) *Log {
	var (
		logger             *zap.Logger
		infoHook, warnHook io.Writer
		wsInfo             []zapcore.WriteSyncer
		wsWarn             []zapcore.WriteSyncer
	)

	if c.Encoding == "" {
		c.Encoding = _defaultEncoding
	}
	encoder := _encoderNameToConstructor[c.Encoding]

	encodeTime := zapcore.ISO8601TimeEncoder
	if customEncodeTime {
		encodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05"))
		}
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        timeKey,
		LevelKey:       levelKey,
		NameKey:        "logger",
		CallerKey:      "file",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     encodeTime,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.FullCallerEncoder,
	}
	if shortCaller {
		encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	}

	if c.CloseDisplay == 0 {
		wsInfo = append(wsInfo, zapcore.AddSync(os.Stdout))
		wsWarn = append(wsWarn, zapcore.AddSync(os.Stdout))
	}

	// zapcore WriteSyncer setting
	if c.isOutput() {
		switch c.Division {
		case TimeDivision:
			infoHook = c.timeDivisionWriter(c.InfoFilename)
			if c.LevelSeparate {
				warnHook = c.timeDivisionWriter(c.ErrorFilename)
			}
		case SizeDivision:
			infoHook = c.sizeDivisionWriter(c.InfoFilename)
			if c.LevelSeparate {
				warnHook = c.sizeDivisionWriter(c.ErrorFilename)
			}
		}
		wsInfo = append(wsInfo, zapcore.AddSync(infoHook))
	}

	if c.ErrorFilename != "" {
		wsWarn = append(wsWarn, zapcore.AddSync(warnHook))
	}

	opts := make([]zap.Option, 0)
	cos := make([]zapcore.Core, 0)

	if c.LevelSeparate {
		cos = append(
			cos,
			zapcore.NewCore(encoder(encoderConfig), zapcore.NewMultiWriteSyncer(wsInfo...), infoLevel(c.Level)),
			zapcore.NewCore(encoder(encoderConfig), zapcore.NewMultiWriteSyncer(wsWarn...), warnLevel()),
		)
	} else {
		cos = append(
			cos,
			zapcore.NewCore(encoder(encoderConfig), zapcore.NewMultiWriteSyncer(wsInfo...), logLevel(c.Level)),
		)
	}

	opts = append(opts, zap.Development())

	if c.Stacktrace {
		opts = append(opts, zap.AddStacktrace(zapcore.WarnLevel))
	}

	if c.caller {
		opts = append(opts, zap.AddCaller(), zap.AddCallerSkip(c.skip))
	}

	logger = zap.New(zapcore.NewTee(cos...), opts...)

	if c.SentryConfig.DSN != "" {
		// sentrycore??????
		cfg := sentryCoreConfig{
			Level:             zap.ErrorLevel,
			Tags:              c.SentryConfig.Tags,
			DisableStacktrace: !c.SentryConfig.AttachStacktrace,
		}
		// ??????sentry?????????
		sentryClient, err := sentry.NewClient(sentry.ClientOptions{
			Dsn:              c.SentryConfig.DSN,
			Debug:            c.SentryConfig.Debug,
			AttachStacktrace: c.SentryConfig.AttachStacktrace,
			Environment:      c.SentryConfig.Environment,
		})
		if err != nil {
			fmt.Println(err)
		}

		sCore := NewSentryCore(cfg, sentryClient)
		logger = logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return zapcore.NewTee(core, sCore)
		}))
	}

	return &Log{logger}
}

func (c *LogOptions) sizeDivisionWriter(filename string) io.Writer {
	hook := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    c.MaxSize,
		MaxBackups: c.MaxBackups,
		MaxAge:     c.MaxSize,
		Compress:   c.Compress,
	}
	return hook
}

func (c *LogOptions) timeDivisionWriter(filename string) io.Writer {
	hook, err := rotatelogs.New(
		filename+c.TimeUnit.Format(),
		rotatelogs.WithMaxAge(time.Duration(int64(24*time.Hour)*int64(c.MaxAge))),
		rotatelogs.WithRotationTime(c.TimeUnit.RotationGap()),
	)

	if err != nil {
		panic(err)
	}
	return hook
}

func (log *Log) Info(msg string, args ...zap.Field) {
	log.L.Info(msg, args...)
}

func (log *Log) Error(msg string, args ...zap.Field) {
	log.L.Error(msg, args...)
}

func (log *Log) Warn(msg string, args ...zap.Field) {
	log.L.Warn(msg, args...)
}

func (log *Log) Debug(msg string, args ...zap.Field) {
	log.L.Debug(msg, args...)
}

func (log *Log) Fatal(msg string, args ...zap.Field) {
	log.L.Fatal(msg, args...)
}

func (log *Log) Infof(format string, args ...interface{}) {
	logMsg := fmt.Sprintf(format, args...)
	log.L.Info(logMsg)
}

func (log *Log) Errorf(format string, args ...interface{}) {
	logMsg := fmt.Sprintf(format, args...)
	log.L.Error(logMsg)
}

func (log *Log) Warnf(format string, args ...interface{}) {
	logMsg := fmt.Sprintf(format, args...)
	log.L.Warn(logMsg)
}

func (log *Log) Debugf(format string, args ...interface{}) {
	logMsg := fmt.Sprintf(format, args...)
	log.L.Debug(logMsg)
}

func (log *Log) Fatalf(format string, args ...interface{}) {
	logMsg := fmt.Sprintf(format, args...)
	log.L.Fatal(logMsg)
}

func With(k string, v interface{}) zap.Field {
	return zap.Any(k, v)
}

func WithError(err error) zap.Field {
	return zap.NamedError("error", err)
}
