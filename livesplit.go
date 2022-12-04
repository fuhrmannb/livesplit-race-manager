package livesplit_race_manager

import (
	"context"
	multiplexerv1 "github.com/fuhrmannb/livesplit-race-manager/grpc/multiplexer/v1"
	connectv1 "github.com/fuhrmannb/livesplit-race-manager/livesplit/connect/v1"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
	"time"
)

type LiveSplitManager struct {
	conn *grpc.ClientConn

	Multiplexer multiplexerv1.DiscoveryServiceClient
	Connect     connectv1.LiveSplitServiceClient
	LiveSplits  map[string]*LiveSplit

	lsCreate chan string
	lsUpdate chan *liveSplitUpdateMsg
	lsDelete chan string
}

type LiveSplitConnectionState string

const (
	LiveSplitStateDisconnected        LiveSplitConnectionState = "disconnected"
	LiveSplitStateWaitingForLiveSplit LiveSplitConnectionState = "waiting_for_livesplit"
	LiveSplitStateConnected           LiveSplitConnectionState = "connected"
)

const LiveSplitTimeWatchRefreshRate = 250 * time.Millisecond

type LiveSplit struct {
	ID                string                   `json:"id,omitempty"`
	State             LiveSplitConnectionState `json:"state,omitempty"`
	Run               *connectv1.Run           `json:"run,omitempty"`
	Segments          []*connectv1.Segment     `json:"segments,omitempty"`
	CurrentSegment    *connectv1.Segment       `json:"current_segment,omitempty"`
	CurrentTimerPhase connectv1.TimerPhase     `json:"current_timer_phase,omitempty"`
	Time              *connectv1.Time          `json:"time,omitempty"`
}

type liveSplitUpdateMsg struct {
	ID                string
	State             *LiveSplitConnectionState
	Run               **connectv1.Run
	Segments          *[]*connectv1.Segment
	CurrentSegment    **connectv1.Segment
	CurrentTimerPhase *connectv1.TimerPhase
	Time              **connectv1.Time
}

type LiveSplitManagerOpts struct {
	MultiplexerAddress string
}

func NewLiveSplitManager(opts LiveSplitManagerOpts) (*LiveSplitManager, error) {
	conn, err := grpc.Dial(opts.MultiplexerAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	log.Info().Str("multiplexer-address", opts.MultiplexerAddress).Msg("connected to gRPC multiplexer")

	lsm := &LiveSplitManager{
		conn:        conn,
		Multiplexer: multiplexerv1.NewDiscoveryServiceClient(conn),
		Connect:     connectv1.NewLiveSplitServiceClient(conn),
		LiveSplits:  make(map[string]*LiveSplit),
		lsCreate:    make(chan string, 100000),
		lsDelete:    make(chan string, 100000),
		lsUpdate:    make(chan *liveSplitUpdateMsg, 100000),
	}

	// Loop to watch player update channel
	go lsm.updateLSLoop()

	// Retrieve initial list of LiveSplit gRPC Servers
	grpcServerResult, err := lsm.Multiplexer.ListGRPCServer(context.Background(), &multiplexerv1.ListGRPCServerRequest{})
	if err != nil {
		return nil, err
	}
	for _, id := range grpcServerResult.Servers {
		lsm.lsCreate <- id
		lsLog(id).Info().Msg("gRPC multiplexer client connected")
		go lsm.fetchLSInfo(id)
	}

	// Watch LiveSplit gRPC Servers
	go func() {
		for {
			err := lsm.watchgRPCMultiplexers()
			if err != nil {
				log.Error().Err(err).Msg("gRPC multiplexer watching has failed, retrying...")
			}
			time.Sleep(time.Second)
		}
	}()

	return lsm, nil
}

func (lsm *LiveSplitManager) Close() {
	lsm.Close()
}

func (lsm *LiveSplitManager) watchgRPCMultiplexers() error {
	watchLS, err := lsm.Multiplexer.WatchGRPCServer(context.Background(), &multiplexerv1.WatchGRPCServerRequest{})
	if err != nil {
		log.Error().Err(err).Msg("can't watch gRPC multiplexer")
		return err
	}

	for {
		ls, err := watchLS.Recv()
		if err != nil {
			log.Error().Err(err).Msg("error during gRPC multiplexer watch")
			return err
		}

		id := ls.Server
		switch ls.Event {
		case multiplexerv1.WatchGRPCServerResponse_EVENT_UNSPECIFIED:
			log.Warn().Msg("received UNSPECIFIED event for WatchGRPCServerResponse, ignoring...")
		case multiplexerv1.WatchGRPCServerResponse_EVENT_CONNECTED:
			lsm.lsCreate <- id
			lsLog(id).Info().Msg("gRPC multiplexer client connected")
			go lsm.fetchLSInfo(id)
		case multiplexerv1.WatchGRPCServerResponse_EVENT_DISCONNECTED:
			lsm.lsDelete <- id
			lsLog(id).Info().Msg("gRPC multiplexer client disconnected")
		}
	}
}

func (lsm *LiveSplitManager) fetchLSInfo(id string) {
	runResp, err := lsm.Connect.GetRun(connectContext(context.Background(), id), &connectv1.GetRunRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}
	segmentListResp, err := lsm.Connect.ListSegment(connectContext(context.Background(), id), &connectv1.ListSegmentRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}
	currSegmentResp, err := lsm.Connect.GetCurrentSegment(connectContext(context.Background(), id), &connectv1.GetCurrentSegmentRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}
	currTimerPhaseResp, err := lsm.Connect.GetCurrentTimerPhase(connectContext(context.Background(), id), &connectv1.GetCurrentTimerPhaseRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}

	phase := LiveSplitStateConnected
	lsm.lsUpdate <- &liveSplitUpdateMsg{
		ID:                id,
		State:             &phase,
		Run:               &runResp.Run,
		Segments:          &segmentListResp.Segments,
		CurrentSegment:    &currSegmentResp.Segment,
		CurrentTimerPhase: &currTimerPhaseResp.Phase,
	}
	lsLog(id).Info().Msg("LiveSplit connected")

	go lsm.watchLSRun(id)
	go lsm.watchLSSplit(id)
	go lsm.watchTimerPhase(id)
	go lsm.watchTime(id)
}

func (lsm *LiveSplitManager) watchLSRun(id string) {
	var err error
	watchRunClient, err := lsm.Connect.WatchRun(connectContext(context.Background(), id), &connectv1.WatchRunRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}

	for {
		runResp, err := watchRunClient.Recv()
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		segmentListResp, err := lsm.Connect.ListSegment(connectContext(context.Background(), id), &connectv1.ListSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}
		currSegmentResp, err := lsm.Connect.GetCurrentSegment(connectContext(context.Background(), id), &connectv1.GetCurrentSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}
		currTimerPhaseResp, err := lsm.Connect.GetCurrentTimerPhase(connectContext(context.Background(), id), &connectv1.GetCurrentTimerPhaseRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		run := runResp.GetRun()
		lsm.lsUpdate <- &liveSplitUpdateMsg{
			ID:                id,
			Run:               &run,
			Segments:          &segmentListResp.Segments,
			CurrentSegment:    &currSegmentResp.Segment,
			CurrentTimerPhase: &currTimerPhaseResp.Phase,
		}
	}
}

func (lsm *LiveSplitManager) watchLSSplit(id string) {
	watchSplitClient, err := lsm.Connect.WatchSplit(connectContext(context.Background(), id), &connectv1.WatchSplitRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}

	for {
		_, err := watchSplitClient.Recv()
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		segmentListResp, err := lsm.Connect.ListSegment(connectContext(context.Background(), id), &connectv1.ListSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}
		currSegmentResp, err := lsm.Connect.GetCurrentSegment(connectContext(context.Background(), id), &connectv1.GetCurrentSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		lsm.lsUpdate <- &liveSplitUpdateMsg{
			ID:             id,
			Segments:       &segmentListResp.Segments,
			CurrentSegment: &currSegmentResp.Segment,
		}
	}
}

func (lsm *LiveSplitManager) watchTimerPhase(id string) {
	watchTimerPhase, err := lsm.Connect.WatchTimerPhase(connectContext(context.Background(), id), &connectv1.WatchTimerPhaseRequest{})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}

	for {
		timerPhaseResp, err := watchTimerPhase.Recv()
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		segmentListResp, err := lsm.Connect.ListSegment(connectContext(context.Background(), id), &connectv1.ListSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}
		currSegmentResp, err := lsm.Connect.GetCurrentSegment(connectContext(context.Background(), id), &connectv1.GetCurrentSegmentRequest{})
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}
		lsm.lsUpdate <- &liveSplitUpdateMsg{
			ID:                id,
			Segments:          &segmentListResp.Segments,
			CurrentSegment:    &currSegmentResp.Segment,
			CurrentTimerPhase: &timerPhaseResp.Phase,
		}
	}
}

func (lsm *LiveSplitManager) watchTime(id string) {
	watchTime, err := lsm.Connect.WatchTime(connectContext(context.Background(), id), &connectv1.WatchTimeRequest{
		RefreshRate: durationpb.New(LiveSplitTimeWatchRefreshRate),
	})
	if err != nil {
		lsm.handleLSError(id, err)
		return
	}

	for {
		timeResp, err := watchTime.Recv()
		if err != nil {
			lsm.handleLSError(id, err)
			return
		}

		lsm.lsUpdate <- &liveSplitUpdateMsg{
			ID:   id,
			Time: &timeResp.CurrentTime,
		}
	}
}

func (lsm *LiveSplitManager) updateLSLoop() {
	for {
		select {
		case id := <-lsm.lsCreate:
			lsm.LiveSplits[id] = &LiveSplit{
				ID:    id,
				State: LiveSplitStateDisconnected,
			}

		case id := <-lsm.lsDelete:
			delete(lsm.LiveSplits, id)

		case newP := <-lsm.lsUpdate:
			id := newP.ID

			p, ok := lsm.LiveSplits[id]
			if !ok {
				continue
			}

			oldState := p.State
			if newP.State != nil {
				p.State = *newP.State
			}
			if newP.Run != nil {
				p.Run = *newP.Run
			}
			if newP.Segments != nil {
				p.Segments = *newP.Segments
			}
			if newP.CurrentSegment != nil {
				p.CurrentSegment = *newP.CurrentSegment
			}
			if newP.CurrentTimerPhase != nil {
				p.CurrentTimerPhase = *newP.CurrentTimerPhase
			}
			if newP.Time != nil {
				p.Time = *newP.Time
			}

			lsm.LiveSplits[id] = clearLS(p)

			if oldState != LiveSplitStateWaitingForLiveSplit && p.State == LiveSplitStateWaitingForLiveSplit {
				go lsm.checkLSConnection(id)
			}
		}
	}
}

func clearLS(ls *LiveSplit) *LiveSplit {
	clearSegment := func(r *connectv1.Segment) {
		if r == nil {
			return
		}

		r.BestSegmentTime = nil
		r.PersonalBestSplitTime = nil
	}

	if ls.CurrentSegment != nil {
		clearSegment(ls.CurrentSegment)
	}
	if ls.Segments != nil {
		for _, segment := range ls.Segments {
			clearSegment(segment)
		}
	}

	return ls
}

func (lsm *LiveSplitManager) checkLSConnection(id string) {
	for {
		// Check if the multiplexer is still connected (LS still there)
		// If not, stop the reconnection
		p, ok := lsm.LiveSplits[id]
		if !ok {
			lsLog(id).Debug().Msg("gRPC multiplexer client disconnected")
			return
		}
		if p.State == LiveSplitStateConnected {
			lsLog(id).Debug().Msg("LiveSplit connected, end of reconnection loop")
			return
		}

		lsLog(id).Info().Msg("retrying LiveSplit connection...")
		lsm.fetchLSInfo(id)

		// TODO: constant/param
		time.Sleep(5 * time.Second)
	}
}

func (lsm *LiveSplitManager) handleLSError(id string, err error) {
	if err == nil {
		return
	}

	lsLog(id).Info().Err(err).Msg("LiveSplit disconnected")

	state := LiveSplitStateWaitingForLiveSplit
	var nilRun *connectv1.Run
	var nilSegments []*connectv1.Segment
	var nilCurrentSegments *connectv1.Segment
	var nilTimerPhase connectv1.TimerPhase
	var nilTime *connectv1.Time
	lsm.lsUpdate <- &liveSplitUpdateMsg{
		ID:                id,
		State:             &state,
		Run:               &nilRun,
		Segments:          &nilSegments,
		CurrentSegment:    &nilCurrentSegments,
		CurrentTimerPhase: &nilTimerPhase,
		Time:              &nilTime,
	}
}

func connectContext(ctx context.Context, id string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "client-id", id)
}

func lsLog(id string) *zerolog.Logger {
	logger := log.With().Str("id", id).Logger()
	return &logger
}
