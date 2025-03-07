package rados

import (
	"context"

	"github.com/rs/zerolog"
)

type Svc struct {
	conn RadosConnInterface
}

func New(radosConn RadosConnInterface) (*Svc, error) {
	return &Svc{conn: radosConn}, nil
}

func (s *Svc) ExecMon(ctx context.Context, cmd string) ([]byte, error) {
	logger := zerolog.Ctx(ctx).With().Str("mon_cmd", cmd).Logger()

	logger.Debug().Msg("executing mon command")
	cmdRes, cmdStatus, err := s.conn.MonCommand([]byte(cmd))
	if err != nil {
		logger.Err(err).Str("cmd_status", cmdStatus).Msg("mon command executed with error")
		return nil, err
	}
	if cmdStatus != "" {
		logger.Info().Str("cmd_status", cmdStatus).Msg("mon command executed with status")
	}
	logger.Debug().Str("mod_cmd_res", string(cmdRes)).Msg("mon command executed with success")
	return cmdRes, nil
}

func (s *Svc) ExecMonWithInputBuff(ctx context.Context, cmd string, inputBuffer []byte) ([]byte, error) {
	logger := zerolog.Ctx(ctx).With().Str("mon_cmd", cmd).Logger()

	logger.Debug().Str("mon_cmd_buf", string(inputBuffer)).Msg("executing mon command with input buffer")
	cmdRes, cmdStatus, err := s.conn.MonCommandWithInputBuffer([]byte(cmd), inputBuffer)
	if err != nil {
		logger.Err(err).Str("cmd_status", cmdStatus).Msg("mon command with input buffer executed with error")
		return nil, err
	}
	if cmdStatus != "" {
		logger.Info().Str("cmd_status", cmdStatus).Msg("mon command with input buffer executed with status")
	}
	logger.Debug().Str("mod_cmd_res", string(cmdRes)).Msg("mon command with input buffer executed with success")
	return cmdRes, nil
}

func (s *Svc) ExecMgr(ctx context.Context, cmd string) ([]byte, error) {
	logger := zerolog.Ctx(ctx).With().Str("mgr_cmd", cmd).Logger()

	logger.Debug().Msg("executing mgr command")
	cmdRes, cmdStatus, err := s.conn.MgrCommand([][]byte{[]byte(cmd)})
	if err != nil {
		logger.Err(err).Str("cmd_status", cmdStatus).Msg("mgr command executed with error")
		return nil, err
	}
	if cmdStatus != "" {
		logger.Info().Str("cmd_status", cmdStatus).Msg("mgr command executed with status")
	}
	logger.Debug().Str("mgr_cmd_res", string(cmdRes)).Msg("mgr command executed with success")
	return cmdRes, nil
}

func (s *Svc) Close() {
	s.conn.Shutdown()
}
