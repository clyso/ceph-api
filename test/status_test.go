package test

import (
	"testing"
	"time"

	pb "github.com/clyso/ceph-api/api/gen/grpc/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func Test_List_Ceph_Status(t *testing.T) {
	r := require.New(t)
	client := pb.NewStatusClient(admConn)
	res, err := client.GetCephStatus(tstCtx, &emptypb.Empty{})
	r.NoError(err)
	r.NotEmpty(res)
	r.NotEmpty(res.Fsid)
	r.NotEmpty(res.QuorumAge)
	r.NotEmpty(res.Health)
	r.NotEmpty(res.Osdmap)
	r.NotEmpty(res.Mgrmap)
	r.NotEmpty(res.Monmap)

}

func Test_GetCephMonDump(t *testing.T) {
	r := require.New(t)
	client := pb.NewStatusClient(admConn)

	res, err := client.GetCephMonDump(tstCtx, &emptypb.Empty{})

	r.NoError(err)
	r.NotNil(res)

	// Validate required fields in CephMonDumpResponse
	r.NotEmpty(res.Fsid, "Fsid should not be empty")
	r.NotEmpty(res.Modified, "Modified timestamp should not be empty")
	r.NotEmpty(res.Created, "Created timestamp should not be empty")
	// created time after 1 january 2024
	r.True(res.Created.AsTime().After(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), "Created timestamp should be after 1 January 2024")
	// created time should be less than current time
	r.True(res.Created.AsTime().Before(time.Now()), "Created timestamp should be before current time")
	// modified time should be greater than or equal to created time
	r.True((res.Modified.AsTime().After(res.Created.AsTime()) || res.Modified.AsTime().Equal(res.Created.AsTime())), "Modified timestamp should be after Created timestamp")

	r.NotZero(res.MinMonRelease, "MinMonRelease should not be zero")
	r.NotEmpty(res.MinMonReleaseName, "MinMonReleaseName should not be empty")
	r.NotNil(res.Features, "Features should not be nil")
	r.NotNil(res.Mons, "Mons should not be nil")
	r.NotEmpty(res.Quorum, "Quorum should not be empty")

	// Validate the Features sub-message
	r.NotNil(res.Features.Persistent, "Features.Persistent should not be nil")

	// Validate the Mons repeated field
	for _, mon := range res.Mons {
		r.NotNil(mon.Rank, "Mon.Rank should not be zero")
		r.NotEmpty(mon.Name, "Mon.Name should not be empty")
		r.NotEmpty(mon.Addr, "Mon.Addr should not be empty")
		r.NotEmpty(mon.PublicAddr, "Mon.PublicAddr should not be empty")
		r.NotNil(mon.PublicAddrs, "Mon.PublicAddrs should not be nil")

		// Validate PublicAddrs repeated field
		for _, addrVec := range mon.PublicAddrs.Addrvec {
			r.NotEmpty(addrVec.Type, "AddrVec.Type should not be empty")
			r.NotEmpty(addrVec.Addr, "AddrVec.Addr should not be empty")
		}
	}
}

func Test_GetCephOsdDump(t *testing.T) {
	r := require.New(t)
	client := pb.NewStatusClient(admConn)
	res, err := client.GetCephOsdDump(tstCtx, &emptypb.Empty{})

	r.NoError(err, "GetCephOsdDump should not return an error")
	r.NotNil(res, "Response should not be nil")

	// Top-level validations
	r.NotEmpty(res.Fsid, "Fsid should not be empty")
	r.NotNil(res.Created, "Created timestamp should not be nil")
	r.NotNil(res.Modified, "Modified timestamp should not be nil")
	r.NotNil(res.LastUpChange, "LastUpChange timestamp should not be nil")
	r.NotNil(res.LastInChange, "LastInChange timestamp should not be nil")
	r.NotEmpty(res.Flags, "Flags should not be empty")
	r.NotZero(res.FlagsNum, "FlagsNum should not be zero")
	r.NotEmpty(res.FlagsSet, "FlagsSet should not be empty")
	r.NotZero(res.CrushVersion, "CrushVersion should not be zero")
	r.NotZero(res.FullRatio, "FullRatio should not be zero")
	r.NotZero(res.BackfillfullRatio, "BackfillfullRatio should not be zero")
	r.NotZero(res.NearfullRatio, "NearfullRatio should not be zero")
	r.NotEmpty(res.RequireMinCompatClient, "RequireMinCompatClient should not be empty")
	r.NotEmpty(res.MinCompatClient, "MinCompatClient should not be empty")
	r.NotEmpty(res.RequireOsdRelease, "RequireOsdRelease should not be empty")

	// Timestamp checks
	r.True(res.Created.AsTime().After(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		"Created timestamp should be after 1 January 2024")
	r.True(res.Created.AsTime().Before(time.Now()),
		"Created timestamp should be before current time")

	// modified >= created
	r.True(!res.Modified.AsTime().Before(res.Created.AsTime()),
		"Modified timestamp should be >= Created timestamp")

	// Check the first pool as an example
	if len(res.Pools) != 0 {
		firstPool := res.Pools[0]
		r.NotZero(firstPool.Pool, "Pool number should not be zero")
		r.NotEmpty(firstPool.PoolName, "PoolName should not be empty")
		r.NotNil(firstPool.CreateTime, "Pool CreateTime should not be nil")
		r.NotEmpty(firstPool.FlagsNames, "Pool FlagsNames should not be empty")

		r.True(firstPool.CreateTime.AsTime().After(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
			"Pool CreateTime should be after 1 Jan 2024")
	}
	// Check OSDs
	for i, osd := range res.Osds {
		r.NotEmpty(osd.Uuid, "uuid should not be empty at index %d", i)
		r.NotEmpty(osd.State, "state array should not be empty at index %d", i)
	}

	// Check OSD XInfo
	for i, xinfo := range res.OsdXinfo {
		r.NotZero(xinfo.Osd, "xinfo.osd should not be zero at index %d", i)
		r.NotNil(xinfo.DownStamp, "xinfo.down_stamp should not be nil at index %d", i)
		r.NotZero(xinfo.Features, "xinfo.features should not be zero at index %d", i)
	}
	r.NotEmpty(res.ErasureCodeProfiles, "ErasureCodeProfiles should not be empty")
}

func Test_GetCephPgDump(t *testing.T) {
	r := require.New(t)

	// This would be your gRPC client to test
	client := pb.NewStatusClient(admConn)

	// Execute the call
	res, err := client.GetCephPgDump(tstCtx, &emptypb.Empty{})

	// Basic checks
	r.NoError(err, "GetCephPgDump should not return an error")
	r.NotNil(res, "GetCephPgDump response should not be nil")

	// Check top-level fields
	r.NotNil(res.PgMap, "pg_map should not be nil")

	// Check a few fields from pg_map
	r.NotNil(res.PgMap.Stamp, "pg_map.stamp should not be nil")
	r.NotNil(res.PgMap.PgStatsSum, "pg_stats_sum should not be nil in pg_map")
	r.NotNil(res.PgMap.OsdStatsSum, "osd_stats_sum should not be nil in pg_map")

	// Validate one of the nested fields in pg_stats_sum, e.g., log_size
	r.NotNil(res.PgMap.PgStatsSum.LogSize, "pg_stats_sum.log_size should not be nil")
	if len(res.PgMap.PgStats) > 0 {
		// Check the first PGStat
		pgStat := res.PgMap.PgStats[0]
		r.NotEmpty(pgStat.Pgid, "PGStat.pgid should not be empty")
		r.NotEmpty(pgStat.State, "PGStat.state should not be empty")

		r.NotNil(pgStat.LastFresh, "PGStat.last_fresh should not be nil")
		r.NotNil(pgStat.LastScrubStamp, "PGStat.last_scrub_stamp should not be nil")
		r.NotNil(pgStat.LastDeepScrubStamp, "PGStat.last_deep_scrub_stamp should not be nil")

	} else {

		t.Log("pg_map.pg_stats is empty; skipping PGStat checks.")
	}

	if res.PgMap.PgStatsDelta != nil {
		r.NotNil(res.PgMap.PgStatsDelta.StatSum, "pg_stats_delta.stat_sum should not be nil")
		r.NotNil(res.PgMap.PgStatsDelta.StoreStats, "pg_stats_delta.store_stats should not be nil")
		r.NotNil(res.PgMap.PgStatsDelta.StampDelta, "pg_stats_delta.stamp_delta should not be nil")
	} else {
		t.Log("pg_stats_delta is nil; skipping delta checks.")
	}

	if len(res.PgMap.OsdStats) > 0 {
		osdStat := res.PgMap.OsdStats[0]
		r.NotZero(osdStat.Osd, "osd stats: osd should not be zero")

		// If network_ping_times is present:
		if len(osdStat.NetworkPingTimes) > 0 {
			netPingTime := osdStat.NetworkPingTimes[0]
			r.NotZero(netPingTime.Osd, "osd_stats[0].network_ping_times[0].osd should not be zero")
			r.NotNil(netPingTime.LastUpdate, "osd_stats[0].network_ping_times[0].last_update should not be nil")
		} else {
			t.Log("osd_stats[0].network_ping_times is empty; skipping ping time checks.")
		}
	} else {
		t.Log("osd_stats is empty; skipping OSD stats checks.")
	}

}
