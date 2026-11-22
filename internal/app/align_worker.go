package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// alignWorker owns one Python process for the lifetime of a queue worker.
type alignWorker struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
}

func (w *alignWorker) start(cmd *exec.Cmd) error {
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return err
	}
	w.cmd, w.input, w.output = cmd, input, bufio.NewReader(output)
	return nil
}

func (w *alignWorker) close() {
	if w.cmd == nil {
		return
	}
	_ = w.input.Close()
	_, _ = io.Copy(io.Discard, w.output)
	_ = w.cmd.Wait()
	*w = alignWorker{}
}

func (w *alignWorker) run(args []string, onLine func(string)) error {
	if err := json.NewEncoder(w.input).Encode(args); err != nil {
		w.close()
		return fmt.Errorf("发送对齐任务失败: %w", err)
	}
	for {
		line, err := w.output.ReadString('\n')
		if code, ok := strings.CutPrefix(strings.TrimSpace(line), "[kiralrc-worker-result] "); ok {
			status, parseErr := strconv.Atoi(code)
			if parseErr != nil {
				_ = w.cmd.Process.Kill()
				w.close()
				return fmt.Errorf("无效的对齐任务退出码: %q", code)
			}
			if status != 0 {
				return fmt.Errorf("对齐任务退出码: %d", status)
			}
			return nil
		}
		if line != "" {
			onLine(line)
		}
		if err != nil {
			w.close()
			return fmt.Errorf("对齐进程未返回任务结果: %w", err)
		}
	}
}
