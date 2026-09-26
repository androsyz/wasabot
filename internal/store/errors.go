package store

import "errors"

var (
	ErrNotFound         = errors.New("store: not found")
	ErrEmailTaken       = errors.New("store: email already taken")
	ErrJIDLinked        = errors.New("store: whatsapp number already linked to another client")
	ErrDuplicateMessage = errors.New("store: duplicate message")
)
