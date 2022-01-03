package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	LogPath string
)

type MyHook struct{}

func (h *MyHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
		logrus.TraceLevel,
	}
}

func (h *MyHook) Fire(entry *logrus.Entry) error {
	var file *os.File
	defer file.Close()
	y, m, d := time.Now().Date()
	fileName := filepath.Join(LogPath, fmt.Sprintf("%d-%d-%d.log", y, int(m), d))
	_, err := os.Stat(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			file, err = os.Create(fileName)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		file, err = os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY, os.ModePerm)
		if err != nil {
			return err
		}
	}

	fi, err := file.Stat()
	if err != nil {
		return err
	}
	if fi.Mode() != os.ModePerm {
		if err = file.Chmod(os.ModePerm); err != nil {
			return err
		}
	}

	if _, err = file.Write([]byte(entry.Message)); err != nil {
		return err
	}

	return nil
}
