//go:build windows

package service

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
)

var (
	engineKernel32             = syscall.NewLazyDLL("kernel32.dll")
	createEngineJobObjectW     = engineKernel32.NewProc("CreateJobObjectW")
	setEngineJobInformation    = engineKernel32.NewProc("SetInformationJobObject")
	assignEngineProcessToJob   = engineKernel32.NewProc("AssignProcessToJobObject")
	closeEngineJobObjectHandle = engineKernel32.NewProc("CloseHandle")
)

type engineJobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type engineJobExtendedLimitInformation struct {
	BasicLimitInformation engineJobBasicLimitInformation
	IOInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type windowsProcessTreeJob struct {
	handle uintptr
}

func newProcessTreeAuthority(cmd *exec.Cmd) (processTreeAuthority, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("engine process is not started")
	}
	handle, _, callErr := createEngineJobObjectW.Call(0, 0)
	if handle == 0 {
		return nil, windowsCallError(callErr)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_, _, _ = closeEngineJobObjectHandle.Call(handle)
		}
	}()

	info := engineJobExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, callErr := setEngineJobInformation.Call(
		handle,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if result == 0 {
		return nil, windowsCallError(callErr)
	}

	var assignErr error
	if err := cmd.Process.WithHandle(func(processHandle uintptr) {
		result, _, callErr := assignEngineProcessToJob.Call(handle, processHandle)
		if result == 0 {
			assignErr = windowsCallError(callErr)
		}
	}); err != nil {
		return nil, err
	}
	if assignErr != nil {
		return nil, assignErr
	}
	closeOnError = false
	return &windowsProcessTreeJob{handle: handle}, nil
}

func (j *windowsProcessTreeJob) Kill() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	result, _, callErr := closeEngineJobObjectHandle.Call(j.handle)
	j.handle = 0
	if result == 0 {
		return windowsCallError(callErr)
	}
	return nil
}

func windowsCallError(err error) error {
	if err != nil && err != syscall.Errno(0) {
		return err
	}
	return syscall.EINVAL
}
