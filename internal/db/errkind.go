package db

import (
	"errors"
	"syscall"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Kinds of error a person can act on. A request that fails answers "internal error" (nothing of
// the database goes out to the browser), but these causes have a clear next step, so the answer
// names them: a full disk is freed, a busy database is retried.
const (
	ErrKindDiskFull = "disk_full" // SQLite o el sistema: sin espacio en el disco de los datos
	ErrKindBusy     = "busy"      // la base estuvo ocupada más que busy_timeout
	ErrKindReadOnly = "read_only" // el archivo de la base o su carpeta no se pueden escribir
	ErrKindCorrupt  = "corrupt"   // el archivo está dañado
)

// ErrKind classifies err ("" when it is none of the known causes).
func ErrKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, syscall.ENOSPC) {
		return ErrKindDiskFull
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return ""
	}
	switch se.Code() & 0xff { // el código primario (los extendidos llevan más bits)
	case sqlite3.SQLITE_FULL:
		return ErrKindDiskFull
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return ErrKindBusy
	case sqlite3.SQLITE_READONLY, sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_PERM:
		return ErrKindReadOnly
	case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
		return ErrKindCorrupt
	}
	return ""
}
