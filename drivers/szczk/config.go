package szczk

import (
	"github.com/hejuqingci/OpenList/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var config = driver.Config{
	Name:        "szczk",
	DisplayName: "Szczk Cloud",
	LocalSort:   false,
	NoCache:     true,
	KeepFileInfo: true,
	DefaultRoot: "/",
	// Add any other default configurations here
}

func init() {
	driver.RegisterDriver(&Szczk{})
}

func (d *Szczk) GetStorage() *model.Storage {
	return &d.Storage
}

func (d *Szczk) SetStorage(storage model.Storage) {
	d.Storage = storage
}

