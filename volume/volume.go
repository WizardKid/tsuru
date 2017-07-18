// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package volume

import (
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	"github.com/tsuru/config"
	"github.com/tsuru/tsuru/auth"
	internalConfig "github.com/tsuru/tsuru/config"
	"github.com/tsuru/tsuru/db"
	"github.com/tsuru/tsuru/provision"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var (
	ErrVolumeNotFound     = errors.New("volume not found")
	ErrVolumeAlreadyBound = errors.New("volume already bound in mountpoint")
	ErrVolumeBindNotFound = errors.New("volume bind not found")
)

type BindMode string

const (
	BindModeReadOnly  = BindMode("ro")
	BindModeReadWrite = BindMode("rw")
)

type VolumePlan struct {
	Name string
	Opts map[string]interface{}
}

type VolumeBindID struct {
	App        string
	MountPoint string
	Volume     string
}

type VolumeBind struct {
	ID   VolumeBindID `bson:"_id"`
	Mode BindMode
}

type Volume struct {
	Name      string `bson:"_id"`
	Pool      string
	Plan      VolumePlan
	TeamOwner string
	Status    string
	Opts      map[string]string `bson:",omitempty"`
}

func (v *Volume) UnmarshalPlan(result interface{}) error {
	jsonData, err := json.Marshal(v.Plan.Opts)
	if err != nil {
		return errors.WithStack(err)
	}
	return errors.WithStack(json.Unmarshal(jsonData, result))
}

func (v *Volume) Validate() error {
	if v.Name == "" {
		return errors.New("volume name cannot be empty")
	}
	pool, err := provision.GetPoolByName(v.Pool)
	if err != nil {
		return errors.WithStack(err)
	}
	_, err = auth.GetTeam(v.TeamOwner)
	if err != nil {
		return errors.WithStack(err)
	}
	prov, err := pool.GetProvisioner()
	if err != nil {
		return errors.WithStack(err)
	}
	data, err := config.Get(volumePlanKey(v.Plan.Name, prov.GetName()))
	if err != nil {
		return errors.WithStack(err)
	}
	planOpts, ok := internalConfig.ConvertEntries(data).(map[string]interface{})
	if !ok {
		return errors.Errorf("invalid type for plan opts %T", planOpts)
	}
	v.Plan.Opts = planOpts
	return nil
}

func (v *Volume) Save() error {
	err := v.Validate()
	if err != nil {
		return err
	}
	conn, err := db.Conn()
	if err != nil {
		return errors.WithStack(err)
	}
	defer conn.Close()
	_, err = conn.Volumes().UpsertId(v.Name, v)
	return errors.WithStack(err)
}

func (v *Volume) BindApp(appName, mountPoint string, mode BindMode) error {
	if mode == "" {
		mode = BindModeReadWrite
	}
	if mode != BindModeReadOnly && mode != BindModeReadWrite {
		return errors.Errorf("invalid bind mode, expected %q or %q, got %q", BindModeReadOnly, BindModeReadWrite, mode)
	}
	conn, err := db.Conn()
	if err != nil {
		return errors.WithStack(err)
	}
	defer conn.Close()
	bind := VolumeBind{
		ID: VolumeBindID{
			App:        appName,
			MountPoint: mountPoint,
			Volume:     v.Name,
		},
		Mode: mode,
	}
	err = conn.VolumeBinds().Insert(bind)
	if err != nil && mgo.IsDup(err) {
		return ErrVolumeAlreadyBound
	}
	return errors.WithStack(err)
}

func (v *Volume) UnbindApp(appName, mountPoint string) error {
	conn, err := db.Conn()
	if err != nil {
		return errors.WithStack(err)
	}
	defer conn.Close()
	err = conn.VolumeBinds().RemoveId(VolumeBindID{
		App:        appName,
		Volume:     v.Name,
		MountPoint: mountPoint,
	})
	if err == mgo.ErrNotFound {
		return ErrVolumeBindNotFound
	}
	return errors.WithStack(err)
}

func (v *Volume) Binds() ([]VolumeBind, error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer conn.Close()
	var binds []VolumeBind
	err = conn.VolumeBinds().Find(bson.M{"_id.volume": v.Name}).All(&binds)
	if err != nil {
		return nil, err
	}
	return binds, nil
}

func ListByApp(appName string) ([]Volume, error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer conn.Close()
	var volumeNames []string
	err = conn.VolumeBinds().Find(bson.M{"_id.app": appName}).Distinct("_id.volume", &volumeNames)
	if err != nil {
		return nil, err
	}
	var volumes []Volume
	err = conn.Volumes().Find(bson.M{"_id": bson.M{"$in": volumeNames}}).All(&volumes)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return volumes, nil
}

func Load(name string) (*Volume, error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer conn.Close()
	var v Volume
	err = conn.Volumes().FindId(name).One(&v)
	if err == mgo.ErrNotFound {
		return nil, ErrVolumeNotFound
	}
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &v, nil
}

func volumePlanKey(planName, provisioner string) string {
	return fmt.Sprintf("volume-plans:%s:%s", planName, provisioner)
}