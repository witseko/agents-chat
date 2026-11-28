package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/witseko/agentcy/internal/paths"
)

const (
	// coreLockSuffix ends the path of the lock file every core holds open in its data
	// directory, so the open file names the core's data directory.
	coreLockSuffix = "/core.lock"
	// coreStopTimeout bounds the wait for a core to exit after SIGTERM. The Go core gives
	// running requests 5 seconds, as scripts/stop-core.sh expects.
	coreStopTimeout    = 15 * time.Second
	coreStopPoll       = 200 * time.Millisecond
	processListTimeout = 5 * time.Second
)

// RunningCore is one agentcy core process of the current user.
type RunningCore struct {
	PID int
	// Uptime is the elapsed time as ps prints it, such as 01:02:03 or 2-03:04:05.
	Uptime string
	// DataDir is the directory whose core.lock the process holds, or "" when unknown.
	DataDir string
}

// CoreProcesses finds and stops the running agentcy cores of the current user.
type CoreProcesses interface {
	List() ([]RunningCore, error)
	Stop(pid int) error
}

// systemCores reads processes with ps and lsof and stops a core with SIGTERM, following
// the rules of scripts/stop-core.sh: it matches only processes of this user that run an
// agentcy core command line, and never sends SIGKILL.
type systemCores struct{}

// List returns the agentcy cores of the current user, in ps order.
func (systemCores) List() ([]RunningCore, error) {
	output, err := runTool("ps", "-A", "-ww", "-o", "uid=,pid=,etime=,command=")
	if err != nil {
		return nil, err
	}
	cores := parseCoreProcesses(output, os.Getuid())
	for index := range cores {
		// lsof is optional: without it the data directory shows as unknown.
		files, _ := runTool("lsof", "-a", "-p", strconv.Itoa(cores[index].PID), "-Fn")
		cores[index].DataDir = dataDirFromLsof(files)
	}
	return cores, nil
}

// Stop sends SIGTERM to a core and waits until it exits. It checks first that the process
// is still an agentcy core of this user, so a reused process ID is never signalled.
func (cores systemCores) Stop(pid int) error {
	running, err := cores.List()
	if err != nil {
		return err
	}
	if !containsCore(running, pid) {
		return fmt.Errorf("Process %d is no longer an agentcy core. Nothing was stopped.", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("Could not stop the agentcy core (process %d): %v", pid, err)
	}
	deadline := time.Now().Add(coreStopTimeout)
	for processExists(pid) {
		if time.Now().After(deadline) {
			return fmt.Errorf("The agentcy core (process %d) did not stop within %s.", pid, coreStopTimeout)
		}
		time.Sleep(coreStopPoll)
	}
	return nil
}

// runTool runs a system program found on the absolute PATH entries with a fixed argument
// list, no shell, and a short timeout, and returns its standard output.
func runTool(name string, arguments ...string) (string, error) {
	pathValue, pathSet := os.LookupEnv("PATH")
	program := paths.FindOnPath(name, pathValue, pathSet)
	if program == "" {
		return "", fmt.Errorf("%s is missing. Install %s to list the running cores.", name, name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), processListTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, program, arguments...)
	command.Env = []string{"PATH=" + pathValue, "LC_ALL=C"}
	output, err := command.Output()
	return string(output), err
}

// processExists reports whether a process with this ID still runs.
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// containsCore reports whether the list holds a core with this process ID.
func containsCore(cores []RunningCore, pid int) bool {
	for _, core := range cores {
		if core.PID == pid {
			return true
		}
	}
	return false
}

// parseCoreProcesses reads `ps -o uid=,pid=,etime=,command=` output and keeps the
// processes of userID that run an agentcy core.
func parseCoreProcesses(output string, userID int) []RunningCore {
	var cores []RunningCore
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		uid, uidErr := strconv.Atoi(fields[0])
		pid, pidErr := strconv.Atoi(fields[1])
		if uidErr != nil || pidErr != nil || uid != userID {
			continue
		}
		if isCoreCommand(strings.Join(fields[3:], " ")) {
			cores = append(cores, RunningCore{PID: pid, Uptime: fields[2]})
		}
	}
	return cores
}

// isCoreCommand matches the command lines of scripts/stop-core.sh: `<path>/agentcy core`
// for the Go core and `python ... -m agentcy.mcp.core` for the Python core of older
// versions.
func isCoreCommand(commandLine string) bool {
	return commandLine == "agentcy core" ||
		strings.HasSuffix(commandLine, "/agentcy core") ||
		strings.HasSuffix(commandLine, " -m agentcy.mcp.core")
}

// dataDirFromLsof finds the open core.lock in `lsof -Fn` output and returns its directory.
func dataDirFromLsof(output string) string {
	for _, line := range strings.Split(output, "\n") {
		name, isName := strings.CutPrefix(line, "n")
		if isName && strings.HasSuffix(name, coreLockSuffix) {
			return strings.TrimSuffix(name, coreLockSuffix)
		}
	}
	return ""
}
