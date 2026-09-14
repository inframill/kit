package inframill

import (
	"context"
	"errors"
)

// Backend owns workspace layout and produces independently executable Instances.
type Backend interface {
	ListCommandDefinitions() []CommandDefinition
	// ListDrivers returns cached health without opening Instance storage or probing.
	ListDrivers(context.Context) ([]DriverInfo, error)
	GetAllIDs(context.Context) ([]InstanceID, error)
	InitInstance(context.Context, InstanceID, string, Blueprint, InstanceValues) error
	SpawnInstance(context.Context, InstanceID) (Instance, error)
}

var (
	ErrNotFound       = errors.New("instance or command not found")
	ErrConflict       = errors.New("conflicting request")
	ErrNotStarted     = errors.New("engine has not started")
	ErrQueueFull      = errors.New("instance command queue is full")
	ErrClosed         = errors.New("instance or engine is closing")
	ErrInstanceFailed = errors.New("instance has failed")
	ErrAlreadyExists  = errors.New("instance name or ID already exists")
)
