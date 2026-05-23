package symlink

import (
	"os"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

func Symlink(oldPath, newPath string) (err error) {
	if _, err = os.Lstat(newPath); err == nil {
		if err = os.Remove(newPath); err != nil {
			err = errors.Wrap(err)
			return
		}
	}

	if err = os.Symlink(oldPath, newPath); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}
