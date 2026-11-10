package db

import (
	"errors"
	"strings"
)

// Mantenimiento: las ejecuciones con problemas de todos los proyectos (en curso sin actividad,
// incompletas, cerradas a mano o por inactividad), para revisarlas y cerrarlas o borrarlas en lote.

// Maintenance states, the filter of MaintenanceRuns.
const (
	MaintAll       = ""           // en curso o incompletas
	MaintIdle      = "idle"       // en curso y sin actividad desde IdleBefore
	MaintRunning   = "running"    // en curso
	MaintCut       = "incomplete" // cerradas con tests sin terminar o interrumpidas por el cliente
	MaintByHand    = "manual"     // cerradas a mano desde la interfaz
	MaintByTimeout = "auto"       // cerradas solas por inactividad
)

// MaintenanceFilter selects the runs of the maintenance view. Empty fields do not filter.
type MaintenanceFilter struct {
	State                        string
	Project, Environment, Branch string
	IdleBefore                   int64 // MaintIdle: sin actividad desde antes de esta hora (Unix ms)
	StartedBefore                int64 // solo las que empezaron antes (Unix ms); 0 = cualquiera
	Limit                        int
}

// ErrBadMaintenanceState: a state MaintenanceRuns does not know.
var ErrBadMaintenanceState = errors.New("unknown maintenance state")

// MaxMaintenanceRuns bounds a listing and a bulk action.
const MaxMaintenanceRuns = 200

// MaintenanceRuns lists the runs that match f, newest first, with live counters.
func (s *Store) MaintenanceRuns(f MaintenanceFilter) ([]Run, error) {
	var parts []string
	var args []any
	add := func(cond string, a ...any) { parts = append(parts, cond); args = append(args, a...) }
	switch f.State {
	case MaintAll:
		add("(r.status = 'RUNNING' OR r.incomplete = 1)")
	case MaintIdle:
		add("r.status = 'RUNNING' AND r.last_activity < ?", f.IdleBefore)
	case MaintRunning:
		add("r.status = 'RUNNING'")
	case MaintCut:
		add("r.status != 'RUNNING' AND r.incomplete = 1")
	case MaintByHand:
		add("r.closed_by != '' AND r.closed_by != ?", ClosedByInactivity)
	case MaintByTimeout:
		add("r.closed_by = ?", ClosedByInactivity)
	default:
		return nil, ErrBadMaintenanceState
	}
	for _, c := range []struct{ col, v string }{{"r.project", f.Project}, {"r.environment", f.Environment}, {"r.branch", f.Branch}} {
		if c.v != "" {
			add(c.col+" = ?", c.v)
		}
	}
	if f.StartedBefore > 0 {
		add("r.started_at < ?", f.StartedBefore)
	}
	limit := f.Limit
	if limit <= 0 || limit > MaxMaintenanceRuns {
		limit = MaxMaintenanceRuns
	}
	return s.listRunsWhere(strings.Join(parts, " AND "), args, limit)
}

// DeleteRuns deletes the given runs that are already closed, with all their evidence, and returns
// how many it deleted and the URLs of their screenshots and artifacts (the caller deletes the
// files). A run still RUNNING is skipped: it has to be closed first. Same transaction rules as
// PurgeRunsBefore.
func (s *Store) DeleteRuns(ids []int64) (int, []string, error) {
	if len(ids) == 0 {
		return 0, nil, nil
	}
	if len(ids) > MaxMaintenanceRuns {
		ids = ids[:MaxMaintenanceRuns]
	}
	// de a tandas, como la retención: una selección grande no deja a los clientes esperando
	return s.deleteInBatches(ids, `status != 'RUNNING'`)
}
