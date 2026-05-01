package maplibre

/*
#include <stdint.h>
#include "maplibre_native_c.h"

// mlnGoGetLogTrampoline returns the typed C function pointer for the
// //exported Go log trampoline. The cast is centralised in
// trampolines.c so this file's preamble doesn't duplicate the
// declaration cgo auto-emits in _cgo_export.h.
mln_log_callback mlnGoGetLogTrampoline(void);
*/
import "C"

import (
	"sync"
	"unsafe"
)

// LogSeverity mirrors mln_log_severity.
type LogSeverity uint32

const (
	LogSeverityInfo    LogSeverity = LogSeverity(C.MLN_LOG_SEVERITY_INFO)
	LogSeverityWarning LogSeverity = LogSeverity(C.MLN_LOG_SEVERITY_WARNING)
	LogSeverityError   LogSeverity = LogSeverity(C.MLN_LOG_SEVERITY_ERROR)
)

func (s LogSeverity) String() string {
	switch s {
	case LogSeverityInfo:
		return "INFO"
	case LogSeverityWarning:
		return "WARNING"
	case LogSeverityError:
		return "ERROR"
	}
	return "UNKNOWN"
}

// LogSeverityMask is a bitmask used by SetLogAsyncSeverityMask to
// control which severities mbgl is allowed to dispatch from worker
// threads. Mirrors mln_log_severity_mask.
type LogSeverityMask uint32

const (
	LogSeverityMaskInfo    LogSeverityMask = LogSeverityMask(C.MLN_LOG_SEVERITY_MASK_INFO)
	LogSeverityMaskWarning LogSeverityMask = LogSeverityMask(C.MLN_LOG_SEVERITY_MASK_WARNING)
	LogSeverityMaskError   LogSeverityMask = LogSeverityMask(C.MLN_LOG_SEVERITY_MASK_ERROR)
	// LogSeverityMaskDefault matches mbgl's own default: info and
	// warning may be async; error stays synchronous.
	LogSeverityMaskDefault LogSeverityMask = LogSeverityMask(C.MLN_LOG_SEVERITY_MASK_DEFAULT)
	LogSeverityMaskAll     LogSeverityMask = LogSeverityMask(C.MLN_LOG_SEVERITY_MASK_ALL)
)

// LogEvent mirrors mln_log_event — the category mbgl assigns to a log
// record (parse-style, render, http-request, etc.).
type LogEvent uint32

const (
	LogEventGeneral     LogEvent = LogEvent(C.MLN_LOG_EVENT_GENERAL)
	LogEventSetup       LogEvent = LogEvent(C.MLN_LOG_EVENT_SETUP)
	LogEventShader      LogEvent = LogEvent(C.MLN_LOG_EVENT_SHADER)
	LogEventParseStyle  LogEvent = LogEvent(C.MLN_LOG_EVENT_PARSE_STYLE)
	LogEventParseTile   LogEvent = LogEvent(C.MLN_LOG_EVENT_PARSE_TILE)
	LogEventRender      LogEvent = LogEvent(C.MLN_LOG_EVENT_RENDER)
	LogEventStyle       LogEvent = LogEvent(C.MLN_LOG_EVENT_STYLE)
	LogEventDatabase    LogEvent = LogEvent(C.MLN_LOG_EVENT_DATABASE)
	LogEventHTTPRequest LogEvent = LogEvent(C.MLN_LOG_EVENT_HTTP_REQUEST)
	LogEventSprite      LogEvent = LogEvent(C.MLN_LOG_EVENT_SPRITE)
	LogEventImage       LogEvent = LogEvent(C.MLN_LOG_EVENT_IMAGE)
	LogEventOpenGL      LogEvent = LogEvent(C.MLN_LOG_EVENT_OPENGL)
	LogEventJNI         LogEvent = LogEvent(C.MLN_LOG_EVENT_JNI)
	LogEventAndroid     LogEvent = LogEvent(C.MLN_LOG_EVENT_ANDROID)
	LogEventCrash       LogEvent = LogEvent(C.MLN_LOG_EVENT_CRASH)
	LogEventGlyph       LogEvent = LogEvent(C.MLN_LOG_EVENT_GLYPH)
	LogEventTiming      LogEvent = LogEvent(C.MLN_LOG_EVENT_TIMING)
)

// LogRecord is one log entry produced by mbgl.
type LogRecord struct {
	Severity LogSeverity
	Event    LogEvent
	Code     int64
	Message  string
}

// LogCallback receives a log record. Returning true consumes the
// record; returning false lets mbgl's platform logger handle it as
// well (matching the underlying mln_log_callback contract).
//
// The callback may be invoked from any thread, including while mbgl
// holds internal logging locks. Implementations MUST be thread-safe,
// MUST return quickly, and MUST NOT call back into the maplibre
// package (don't dispatch any Map/Runtime/Session method, don't
// allocate large slices, don't block). Marshal the record into your
// own logging facility and return.
type LogCallback func(LogRecord) bool

// logState is the package-global slot for the registered Go log
// callback. The C API stores its trampoline by reference — there is
// only one slot — so the Go side must match.
var logState struct {
	mu sync.RWMutex
	cb LogCallback
}

//export mlnGoLogTrampoline
func mlnGoLogTrampoline(_ unsafe.Pointer, severity C.uint32_t, event C.uint32_t, code C.int64_t, message *C.char) C.uint32_t {
	logState.mu.RLock()
	cb := logState.cb
	logState.mu.RUnlock()
	if cb == nil {
		return 0
	}
	rec := LogRecord{
		Severity: LogSeverity(severity),
		Event:    LogEvent(event),
		Code:     int64(code),
	}
	if message != nil {
		rec.Message = C.GoString(message)
	}
	if cb(rec) {
		return 1
	}
	return 0
}

// InstallLogCallback registers a process-global log callback. Replaces
// any previously installed callback. Safe to call from any goroutine,
// but the registration itself crosses the cgo boundary so prefer to
// install once at process startup.
//
// See LogCallback for the rules the callback must follow.
func InstallLogCallback(cb LogCallback) error {
	if cb == nil {
		return ClearLogCallback()
	}
	logState.mu.Lock()
	logState.cb = cb
	logState.mu.Unlock()
	if status := C.mln_log_set_callback(C.mlnGoGetLogTrampoline(), nil); status != C.MLN_STATUS_OK {
		// Roll back the slot so a future caller doesn't see a stale cb.
		logState.mu.Lock()
		logState.cb = nil
		logState.mu.Unlock()
		return statusError("mln_log_set_callback", status)
	}
	return nil
}

// ClearLogCallback drops the process-global log callback so future
// records flow through mbgl's platform logger only. Idempotent.
func ClearLogCallback() error {
	if status := C.mln_log_clear_callback(); status != C.MLN_STATUS_OK {
		return statusError("mln_log_clear_callback", status)
	}
	logState.mu.Lock()
	logState.cb = nil
	logState.mu.Unlock()
	return nil
}

// SetLogAsyncSeverityMask controls which severities mbgl is allowed
// to dispatch asynchronously (from worker threads). Pass
// LogSeverityMaskDefault to restore mbgl's default behavior.
//
// Use LogSeverityMaskAll if you want every record to flow through
// your callback regardless of severity, or 0 to force all dispatch
// synchronous on the producing thread.
func SetLogAsyncSeverityMask(mask LogSeverityMask) error {
	if status := C.mln_log_set_async_severity_mask(C.uint32_t(mask)); status != C.MLN_STATUS_OK {
		return statusError("mln_log_set_async_severity_mask", status)
	}
	return nil
}
