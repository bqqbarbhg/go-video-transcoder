package ownedfile

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"
)

type permissionDeniedError struct {
	file string
}

func (self *permissionDeniedError) Error() string {
	return fmt.Sprintf("ownedfile %s: Permission deined, owned by another user", self.file)
}

func IsPermissionDenied(err error) bool {
	switch err.(type) {
	case *permissionDeniedError:
		return true
	default:
		return false
	}
}

func getOwnerPath(file string) string {
	return file + ".owner"
}

func unsafeReadOwner(file string) (string, error) {
	ownerUserBytes, err := ioutil.ReadFile(getOwnerPath(file))
	if err != nil {
		return "", err
	}

	return string(ownerUserBytes), err
}

func unsafeCreateOwner(file string, owner string) error {
	ownerPath := getOwnerPath(file)

	_, err := os.Stat(ownerPath)
	if err == nil {
		return &permissionDeniedError{
			file: file,
		}
	}

	ownerFile, err := os.Create(ownerPath)
	if err != nil {
		return err
	}

	_, err = ownerFile.WriteString(string(owner))
	if err != nil {
		_ = ownerFile.Close()
		_ = os.Remove(ownerPath)
		return err
	}

	err = ownerFile.Close()
	if err != nil {
		_ = os.Remove(ownerPath)
		return err
	}

	return nil
}

func unsafeCheckOwner(file string, owner string) error {
	fileOwner, err := unsafeReadOwner(file)

	if err != nil {
		return err
	}

	if fileOwner != owner {
		return &permissionDeniedError{
			file: file,
		}
	}

	return nil
}

type Collection struct {
	mutex sync.Mutex
}

func (self *Collection) lock() {
	self.mutex.Lock()
}

func (self *Collection) unlock() {
	self.mutex.Unlock()
}

func NewCollection() *Collection {
	return &Collection{
		mutex: sync.Mutex{},
	}
}

// Create a new owned file
func (self *Collection) Create(path string, owner string) error {
	self.lock()
	defer self.unlock()

	return unsafeCreateOwner(path, owner)
}

// Move unowned file to an owned one
// Note: The owned file needs to be created first using `Create`
func (self *Collection) Move(src string, path string, owner string) error {
	self.lock()
	defer self.unlock()

	err := unsafeCheckOwner(path, owner)
	if err != nil {
		return err
	}

	return os.Rename(src, path)
}

func (self *Collection) Delete(path string, owner string) error {
	self.lock()
	defer self.unlock()

	err := unsafeCheckOwner(path, owner)
	if err != nil {
		return err
	}

	// Remove the data file first and cancel deletion if it fails, _but_ the file
	// not existing is not treated as an error, since ownerfiles can exist without
	// data files.
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return os.Remove(getOwnerPath(path))
}

func (self *Collection) ReadOwner(path string) (string, error) {
	self.lock()
	defer self.unlock()
	return unsafeReadOwner(path)
}
