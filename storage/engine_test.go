// Copyright JAMF Software, LLC

package storage

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pvfs "github.com/cockroachdb/pebble/vfs"
	"github.com/jamf/regatta/proto"
	"github.com/jamf/regatta/storage/cluster"
	"github.com/jamf/regatta/storage/logreader"
	"github.com/jamf/regatta/storage/tables"
	lvfs "github.com/lni/vfs"
	"github.com/stretchr/testify/require"
)

func TestEngine_Start(t *testing.T) {
	type fields struct {
		cfg Config
	}
	tests := []struct {
		name        string
		fields      fields
		wantStarted bool
		wantErr     require.ErrorAssertionFunc
	}{
		{
			name:        "start test node",
			fields:      fields{cfg: newTestConfig()},
			wantStarted: true,
			wantErr:     require.NoError,
		},
		{
			name: "start test node pebble logdb",
			fields: fields{cfg: func() Config {
				cfg := newTestConfig()
				cfg.LogDBImplementation = Pebble
				return cfg
			}()},
			wantStarted: true,
			wantErr:     require.NoError,
		},
		{
			name: "start test node checkpoint snapshot",
			fields: fields{cfg: func() Config {
				cfg := newTestConfig()
				cfg.Table.RecoveryType = tables.RecoveryTypeCheckpoint
				return cfg
			}()},
			wantStarted: true,
			wantErr:     require.NoError,
		},
		{
			name: "start test node snapshot snapshot",
			fields: fields{cfg: func() Config {
				cfg := newTestConfig()
				cfg.Table.RecoveryType = tables.RecoveryTypeSnapshot
				return cfg
			}()},
			wantStarted: true,
			wantErr:     require.NoError,
		},
		{
			name: "start tmp fs",
			fields: fields{cfg: func() Config {
				raftPort := getTestPort()
				gossipPort := getTestPort()
				tmpdir := t.TempDir()
				return Config{
					NodeID:         1,
					InitialMembers: map[uint64]string{1: fmt.Sprintf("127.0.0.1:%d", raftPort)},
					NodeHostDir:    filepath.Join(tmpdir, "nh"),
					RTTMillisecond: 5,
					RaftAddress:    fmt.Sprintf("127.0.0.1:%d", raftPort),
					Gossip:         GossipConfig{BindAddress: fmt.Sprintf("127.0.0.1:%d", gossipPort)},
					Table:          TableConfig{DataDir: filepath.Join(tmpdir, "data"), TableCacheSize: 1024, ElectionRTT: 10, HeartbeatRTT: 1},
					Meta:           MetaConfig{ElectionRTT: 10, HeartbeatRTT: 1},
				}
			}()},
			wantStarted: true,
			wantErr:     require.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEngine(tt.fields.cfg)
			defer func() {
				require.NoError(t, e.Close())
			}()
			tt.wantErr(t, e.Start())
			if tt.wantStarted {
				require.NoError(t, e.WaitUntilReady())
			}
		})
	}
}

func TestEngine_Range(t *testing.T) {
	type args struct {
		ctx context.Context
		req *proto.RangeRequest
	}
	tests := []struct {
		name    string
		args    args
		prepare func(e *Engine)
		want    *proto.RangeResponse
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:    "table not found",
			prepare: func(e *Engine) {},
			args: args{
				ctx: context.Background(),
				req: &proto.RangeRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			wantErr: require.Error,
		},
		{
			name: "key not found serializable request",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
			},
			args: args{
				ctx: context.Background(),
				req: &proto.RangeRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			wantErr: require.Error,
		},
		{
			name: "key not found linearizable request",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
			},
			args: args{
				ctx: context.Background(),
				req: &proto.RangeRequest{
					Table:        []byte("table"),
					Key:          []byte("key"),
					Linearizable: true,
				},
			},
			wantErr: require.Error,
		},
		{
			name: "key found serializable request",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.Background(), &proto.PutRequest{Table: []byte("table"), Key: []byte("key"), Value: []byte("value")})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.RangeRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			want: &proto.RangeResponse{
				Header: &proto.ResponseHeader{
					ReplicaId: 1,
					ShardId:   10001,
				},
				Kvs: []*proto.KeyValue{
					{Key: []byte("key"), Value: []byte("value")},
				},
				Count: 1,
			},
			wantErr: require.NoError,
		},
		{
			name: "key found linearizable request",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.Background(), &proto.PutRequest{Table: []byte("table"), Key: []byte("key"), Value: []byte("value")})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.RangeRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			want: &proto.RangeResponse{
				Header: &proto.ResponseHeader{
					ReplicaId: 1,
					ShardId:   10001,
				},
				Kvs: []*proto.KeyValue{
					{Key: []byte("key"), Value: []byte("value")},
				},
				Count: 1,
			},
			wantErr: require.NoError,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newTestEngine(newTestConfig())
			defer e.Close()
			require.NoError(t, e.Start())
			require.NoError(t, e.WaitUntilReady())
			tt.prepare(e)
			got, err := e.Range(tt.args.ctx, tt.args.req)
			tt.wantErr(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEngine_Put(t *testing.T) {
	type args struct {
		ctx context.Context
		req *proto.PutRequest
	}
	tests := []struct {
		name    string
		args    args
		prepare func(e *Engine)
		want    *proto.PutResponse
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:    "table not found",
			prepare: func(e *Engine) {},
			args: args{
				ctx: context.Background(),
				req: &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			wantErr: require.Error,
		},
		{
			name: "put new key",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
			},
			args: args{
				ctx: context.Background(),
				req: &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				},
			},
			want: &proto.PutResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  3,
				},
			},
			wantErr: require.NoError,
		},
		{
			name: "overwrite key",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value2"),
				},
			},
			want: &proto.PutResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
			},
			wantErr: require.NoError,
		},
		{
			name: "overwrite key and get prev",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.PutRequest{
					Table:  []byte("table"),
					Key:    []byte("key"),
					Value:  []byte("value2"),
					PrevKv: true,
				},
			},
			want: &proto.PutResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				PrevKv: &proto.KeyValue{
					Key:   []byte("key"),
					Value: []byte("value"),
				},
			},
			wantErr: require.NoError,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newTestEngine(newTestConfig())
			defer e.Close()
			require.NoError(t, e.Start())
			require.NoError(t, e.WaitUntilReady())
			tt.prepare(e)
			got, err := e.Put(tt.args.ctx, tt.args.req)
			tt.wantErr(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEngine_Delete(t *testing.T) {
	type args struct {
		ctx context.Context
		req *proto.DeleteRangeRequest
	}
	tests := []struct {
		name    string
		args    args
		prepare func(e *Engine)
		want    *proto.DeleteRangeResponse
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:    "table not found",
			prepare: func(e *Engine) {},
			args: args{
				ctx: context.Background(),
				req: &proto.DeleteRangeRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
				},
			},
			wantErr: require.Error,
		},
		{
			name: "delete all",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
			},
			args: args{
				ctx: context.Background(),
				req: &proto.DeleteRangeRequest{
					Table:    []byte("table"),
					Key:      []byte{0},
					RangeEnd: []byte{0},
				},
			},
			want: &proto.DeleteRangeResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  3,
				},
			},
			wantErr: require.NoError,
		},
		{
			name: "delete all and count",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.DeleteRangeRequest{
					Table:    []byte("table"),
					Key:      []byte{0},
					RangeEnd: []byte{0},
					Count:    true,
				},
			},
			want: &proto.DeleteRangeResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				Deleted: 1,
			},
			wantErr: require.NoError,
		},
		{
			name: "delete all and get prev",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.DeleteRangeRequest{
					Table:    []byte("table"),
					Key:      []byte{0},
					RangeEnd: []byte{0},
					PrevKv:   true,
				},
			},
			want: &proto.DeleteRangeResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				Deleted: 1,
				PrevKvs: []*proto.KeyValue{
					{Key: []byte("key"), Value: []byte("value")},
				},
			},
			wantErr: require.NoError,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newTestEngine(newTestConfig())
			defer e.Close()
			require.NoError(t, e.Start())
			require.NoError(t, e.WaitUntilReady())
			tt.prepare(e)
			got, err := e.Delete(tt.args.ctx, tt.args.req)
			tt.wantErr(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEngine_Txn(t *testing.T) {
	type args struct {
		ctx context.Context
		req *proto.TxnRequest
	}
	tests := []struct {
		name    string
		args    args
		prepare func(e *Engine)
		want    *proto.TxnResponse
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:    "table not found",
			prepare: func(e *Engine) {},
			args: args{
				ctx: context.Background(),
				req: &proto.TxnRequest{
					Table: []byte("table"),
				},
			},
			wantErr: require.Error,
		},
		{
			name: "put new key no comp",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.TxnRequest{
					Table: []byte("table"),
					Success: []*proto.RequestOp{{Request: &proto.RequestOp_RequestPut{
						RequestPut: &proto.RequestOp_Put{
							Key:   []byte("key2"),
							Value: []byte("value2"),
						},
					}}},
				},
			},
			want: &proto.TxnResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				Succeeded: true,
				Responses: []*proto.ResponseOp{{Response: &proto.ResponseOp_ResponsePut{
					ResponsePut: &proto.ResponseOp_Put{},
				}}},
			},
			wantErr: require.NoError,
		},
		{
			name: "put new key success comp",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.TxnRequest{
					Table: []byte("table"),
					Compare: []*proto.Compare{{
						Result:      proto.Compare_EQUAL,
						Target:      proto.Compare_VALUE,
						Key:         []byte("key"),
						TargetUnion: &proto.Compare_Value{Value: []byte("value")},
					}},
					Success: []*proto.RequestOp{{Request: &proto.RequestOp_RequestPut{
						RequestPut: &proto.RequestOp_Put{
							Key:   []byte("key2"),
							Value: []byte("value2"),
						},
					}}},
				},
			},
			want: &proto.TxnResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				Succeeded: true,
				Responses: []*proto.ResponseOp{{Response: &proto.ResponseOp_ResponsePut{
					ResponsePut: &proto.ResponseOp_Put{},
				}}},
			},
			wantErr: require.NoError,
		},
		{
			name: "put new key failed cond",
			prepare: func(e *Engine) {
				_ = e.CreateTable("table")
				time.Sleep(1 * time.Second)
				_, _ = e.Put(context.TODO(), &proto.PutRequest{
					Table: []byte("table"),
					Key:   []byte("key"),
					Value: []byte("value"),
				})
			},
			args: args{
				ctx: context.Background(),
				req: &proto.TxnRequest{
					Table: []byte("table"),
					Compare: []*proto.Compare{{
						Result:      proto.Compare_EQUAL,
						Target:      proto.Compare_VALUE,
						Key:         []byte("key"),
						TargetUnion: &proto.Compare_Value{Value: []byte("foo")},
					}},
					Success: []*proto.RequestOp{{Request: &proto.RequestOp_RequestPut{
						RequestPut: &proto.RequestOp_Put{
							Key:   []byte("key2"),
							Value: []byte("value2"),
						},
					}}},
					Failure: []*proto.RequestOp{{Request: &proto.RequestOp_RequestPut{
						RequestPut: &proto.RequestOp_Put{
							Key:    []byte("key"),
							Value:  []byte("value2"),
							PrevKv: true,
						},
					}}},
				},
			},
			want: &proto.TxnResponse{
				Header: &proto.ResponseHeader{
					ShardId:   10001,
					ReplicaId: 1,
					Revision:  4,
				},
				Succeeded: false,
				Responses: []*proto.ResponseOp{{Response: &proto.ResponseOp_ResponsePut{
					ResponsePut: &proto.ResponseOp_Put{
						PrevKv: &proto.KeyValue{Key: []byte("key"), Value: []byte("value")},
					},
				}}},
			},
			wantErr: require.NoError,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newTestEngine(newTestConfig())
			defer e.Close()
			require.NoError(t, e.Start())
			require.NoError(t, e.WaitUntilReady())
			tt.prepare(e)
			got, err := e.Txn(tt.args.ctx, tt.args.req)
			tt.wantErr(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNew(t *testing.T) {
	type args struct {
		cfg Config
	}
	tests := []struct {
		name    string
		args    args
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:    "basic",
			args:    args{cfg: newTestConfig()},
			wantErr: require.NoError,
		},
		{
			name: "missing gossip config",
			args: args{cfg: func() Config {
				cfg := newTestConfig()
				cfg.Gossip = GossipConfig{}
				return cfg
			}()},
			wantErr: require.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := New(tt.args.cfg)
			tt.wantErr(t, err)
			if e != nil {
				_ = e.Close()
			}
		})
	}
}

func newTestConfig() Config {
	fs := lvfs.NewMem()
	raftPort := getTestPort()
	gossipPort := getTestPort()
	return Config{
		NodeID:         1,
		InitialMembers: map[uint64]string{1: fmt.Sprintf("127.0.0.1:%d", raftPort)},
		WALDir:         "/wal",
		NodeHostDir:    "/nh",
		RTTMillisecond: 5,
		RaftAddress:    fmt.Sprintf("127.0.0.1:%d", raftPort),
		Gossip:         GossipConfig{BindAddress: fmt.Sprintf("127.0.0.1:%d", gossipPort), InitialMembers: []string{fmt.Sprintf("127.0.0.1:%d", gossipPort)}},
		Table:          TableConfig{FS: wrapFS(fs), TableCacheSize: 1024, ElectionRTT: 10, HeartbeatRTT: 1},
		Meta:           MetaConfig{ElectionRTT: 10, HeartbeatRTT: 1},
		FS:             fs,
	}
}

func newTestEngine(cfg Config) *Engine {
	e := &Engine{cfg: cfg}
	nh, err := createNodeHost(cfg, e, e)
	if err != nil {
		panic(err)
	}
	e.NodeHost = nh
	e.LogReader = &logreader.LogReader{LogQuerier: nh}
	e.Cluster, err = cluster.New(cfg.Gossip.BindAddress, cfg.Gossip.AdvertiseAddress, e.clusterInfo)
	if err != nil {
		panic(err)
	}
	e.Manager = tables.NewManager(nh, cfg.InitialMembers, tables.Config{
		NodeID: cfg.NodeID,
		Table:  tables.TableConfig(cfg.Table),
		Meta:   tables.MetaConfig(cfg.Meta),
	})
	return e
}

func getTestPort() int {
	l, _ := net.Listen("tcp", ":0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// wrapFS creates a new pebble/vfs.FS instance.
func wrapFS(fs lvfs.FS) pvfs.FS {
	return &pebbleFSAdapter{fs}
}

// pebbleFSAdapter is a wrapper struct that implements the pebble/vfs.FS interface.
type pebbleFSAdapter struct {
	fs lvfs.FS
}

// GetDiskUsage ...
func (p *pebbleFSAdapter) GetDiskUsage(path string) (pvfs.DiskUsage, error) {
	du, err := p.fs.GetDiskUsage(path)
	return pvfs.DiskUsage{
		AvailBytes: du.AvailBytes,
		TotalBytes: du.TotalBytes,
		UsedBytes:  du.UsedBytes,
	}, err
}

// Create ...
func (p *pebbleFSAdapter) Create(name string) (pvfs.File, error) {
	return p.fs.Create(name)
}

// Link ...
func (p *pebbleFSAdapter) Link(oldname, newname string) error {
	return p.fs.Link(oldname, newname)
}

// Open ...
func (p *pebbleFSAdapter) Open(name string, opts ...pvfs.OpenOption) (pvfs.File, error) {
	f, err := p.fs.Open(name)
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt.Apply(f)
	}
	return f, nil
}

// OpenDir ...
func (p *pebbleFSAdapter) OpenDir(name string) (pvfs.File, error) {
	return p.fs.OpenDir(name)
}

// Remove ...
func (p *pebbleFSAdapter) Remove(name string) error {
	return p.fs.Remove(name)
}

// RemoveAll ...
func (p *pebbleFSAdapter) RemoveAll(name string) error {
	return p.fs.RemoveAll(name)
}

// Rename ...
func (p *pebbleFSAdapter) Rename(oldname, newname string) error {
	return p.fs.Rename(oldname, newname)
}

// ReuseForWrite ...
func (p *pebbleFSAdapter) ReuseForWrite(oldname, newname string) (pvfs.File, error) {
	return p.fs.ReuseForWrite(oldname, newname)
}

// MkdirAll ...
func (p *pebbleFSAdapter) MkdirAll(dir string, perm os.FileMode) error {
	return p.fs.MkdirAll(dir, perm)
}

// Lock ...
func (p *pebbleFSAdapter) Lock(name string) (io.Closer, error) {
	return p.fs.Lock(name)
}

// List ...
func (p *pebbleFSAdapter) List(dir string) ([]string, error) {
	return p.fs.List(dir)
}

// Stat ...
func (p *pebbleFSAdapter) Stat(name string) (os.FileInfo, error) {
	return p.fs.Stat(name)
}

// PathBase ...
func (p *pebbleFSAdapter) PathBase(path string) string {
	return p.fs.PathBase(path)
}

// PathJoin ...
func (p *pebbleFSAdapter) PathJoin(elem ...string) string {
	return p.fs.PathJoin(elem...)
}

// PathDir ...
func (p *pebbleFSAdapter) PathDir(path string) string {
	return p.fs.PathDir(path)
}
