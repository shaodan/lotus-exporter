package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-jsonrpc"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/api/apistruct"
	"github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/actors/adt"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/types"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	port     uint
	interval time.Duration
	minerID  string
	height   int64
	infoJson []byte

	daemonAPI apistruct.FullNodeStruct
	minerAddr address.Address

	totalRawBytePower = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "total_raw_byte_power",
		Help: "Total raw byte power of network",
	})
	totalQualityPower = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "total_quality_power",
		Help: "Total quality power of network",
	})
	minerRawBytePower = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "miner_raw_byte_power",
		Help: "Raw byte power of Miner",
	})
	minerQualityPower = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "miner_quality_power",
		Help: "Quality power of miner",
	})
	expectWinPerDay = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "expect_win_per_day",
		Help: "Expectation of wining blocks per day",
	})
	sectorsCommitted = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sectors_committed",
		Help: "The number of committed sectors",
	})
	sectorsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sectors_active",
		Help: "The number of active sectors",
	})
	sectorsFaulty = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sectors_faulty",
		Help: "The number of faulty sectors",
	})
	workerBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "worker_balance",
		Help: "Balance of worker address",
	})
	minerBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "miner_balance",
		Help: "Balance of miner address",
	})
	controlBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "control_balance",
		Help: "Balance of control address",
	})
	availableBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "available_balance",
		Help: "Balance available",
	})
	pledgedBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pledged_balance",
		Help: "Balance of pledged",
	})
	preCommitBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "precommit_balance",
		Help: "Balance of precommit",
	})
	vestingBalance = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vesting_balance",
		Help: "Balance vesting",
	})
)

type MinerInfo struct {
	MinerID string
	// Power
	TotalRawBytePower float64
	TotalQualityPower float64
	MinerRawBytePower float64
	MinerQualityPower float64
	// Expectation
	WinPerDay float64
	// Sectors
	SectorsCommitted float64
	SectorsActive    float64
	SectorsFaulty    float64
	// Balance
	WorkerBalance    float64
	ControlBalance   float64
	MinerBalance     float64
	AvailableBalance float64
	PledgedBalance   float64
	PreCommitBalance float64
	VestingBalance   float64
}

func init() {
	flag.DurationVar(&interval, "i", 1*time.Minute, "Interval of refreshing miner info")
	flag.StringVar(&minerID, "m", "", "Miner ID, required!")
	flag.Int64Var(&height, "t", 0, "Target height, default latest")
	flag.UintVar(&port, "p", 9002, "Port, default 9002")

	// disable go collector
	prometheus.Unregister(prometheus.NewGoCollector())
	prometheus.MustRegister(totalRawBytePower, totalQualityPower)
	prometheus.MustRegister(minerRawBytePower, minerQualityPower)
	prometheus.MustRegister(expectWinPerDay)
	prometheus.MustRegister(sectorsCommitted, sectorsFaulty, sectorsActive)
	prometheus.MustRegister(workerBalance, minerBalance, controlBalance)
	prometheus.MustRegister(availableBalance, pledgedBalance, vestingBalance, preCommitBalance)
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(infoJson)
}

func main() {
	flag.Parse()

	if len(minerID) == 0 {
		log.Fatalln("Please input miner ID")
	}

	header := http.Header{}
	addr := "https://api.node.glif.io/rpc/v0"

	ctx := context.Background()
	closer, err := jsonrpc.NewMergeClient(ctx,
		addr,
		"Filecoin",
		[]interface{}{&daemonAPI.Internal, &daemonAPI.CommonStruct.Internal},
		header,
	)
	if err != nil {
		log.Fatalf("connecting with lotus failed: %s", err)
	}
	defer closer()

	minerAddr, err = address.NewFromString(minerID)
	if err != nil {
		log.Fatalf("wrong actor address: %s", minerAddr)
	}

	log.Printf("get miner %s's info", minerID)

	var tpKey types.TipSetKey
	if height == 0 {
		tpKey = types.EmptyTSK
		log.Println("Target height: Head")
	} else {
		ts, err := daemonAPI.ChainGetTipSetByHeight(context.Background(), abi.ChainEpoch(height), types.EmptyTSK)
		if err != nil {
			log.Fatalf("wrong height: %s", err)
		}
		tpKey = ts.Key()
		log.Printf("Target height: %d, tsKey: %s\n", height, tpKey)
		// tpKey = types.NewTipSetKey(c)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for ; true; <-ticker.C {
			info, err := GetMinerInfo(tpKey)
			if err != nil {
				log.Printf("get info err %s\n", err)
				continue
			}
			totalRawBytePower.Set(info.TotalRawBytePower)
			totalQualityPower.Set(info.TotalQualityPower)
			minerRawBytePower.Set(info.MinerRawBytePower)
			minerQualityPower.Set(info.MinerQualityPower)
			expectWinPerDay.Set(info.WinPerDay)
			sectorsCommitted.Set(info.SectorsCommitted)
			sectorsActive.Set(info.SectorsActive)
			sectorsFaulty.Set(info.SectorsFaulty)
			minerBalance.Set(info.MinerBalance)
			workerBalance.Set(info.WorkerBalance)
			controlBalance.Set(info.ControlBalance)
			availableBalance.Set(info.AvailableBalance)
			pledgedBalance.Set(info.PledgedBalance)
			preCommitBalance.Set(info.PreCommitBalance)
			vestingBalance.Set(info.VestingBalance)

			infoJson, err = json.Marshal(info)
			if err != nil {
				log.Printf("get info err %s\n", err)
				continue
			}
			log.Println(string(infoJson))
		}
	}()

	http.HandleFunc("/json", handler)
	http.Handle("/metrics", promhttp.Handler())
	listenAddr := fmt.Sprintf(":%d", port)
	log.Printf("listen on %s\n", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Panicf("error starting HTTP server %s", err)
	}
}

func GetMinerInfo(tpKey types.TipSetKey) (info MinerInfo, err error) {
	ctx := context.Background()

	mact, err := daemonAPI.StateGetActor(ctx, minerAddr, tpKey)
	if err != nil {
		return
	}

	tbs := blockstore.NewTieredBstore(blockstore.NewAPIBlockstore(&daemonAPI), blockstore.NewMemory())
	mas, err := miner.Load(adt.WrapStore(ctx, cbor.NewCborStore(tbs)), mact)
	if err != nil {
		return
	}

	mi, err := daemonAPI.StateMinerInfo(ctx, minerAddr, tpKey)
	if err != nil {
		return
	}
	log.Printf("Sector Size: %s\n", types.SizeStr(types.NewInt(uint64(mi.SectorSize))))

	pow, err := daemonAPI.StateMinerPower(ctx, minerAddr, tpKey)
	if err != nil {
		return
	}
	info.MinerID = minerID
	info.MinerRawBytePower = ConvertPower(pow.MinerPower.RawBytePower)
	info.MinerQualityPower = ConvertPower(pow.MinerPower.QualityAdjPower)
	info.TotalRawBytePower = ConvertPower(pow.TotalPower.RawBytePower)
	info.TotalQualityPower = ConvertPower(pow.TotalPower.QualityAdjPower)

	secCounts, err := daemonAPI.StateMinerSectorCount(ctx, minerAddr, tpKey)
	if err != nil {
		return
	}

	info.SectorsCommitted = float64(secCounts.Live)
	info.SectorsActive = float64(secCounts.Active)
	info.SectorsFaulty = float64(secCounts.Faulty)

	if !pow.HasMinPower {
		info.WinPerDay = 0
	} else {
		qpercI := types.BigDiv(types.BigMul(pow.MinerPower.QualityAdjPower, types.NewInt(1000000)), pow.TotalPower.QualityAdjPower)
		expWinChance := float64(types.BigMul(qpercI, types.NewInt(build.BlocksPerEpoch)).Int64()) / 1000000
		if expWinChance > 0 {
			if expWinChance > 1 {
				expWinChance = 1
			}
			winRate := time.Duration(float64(time.Second*time.Duration(build.BlockDelaySecs)) / expWinChance)
			info.WinPerDay = float64(time.Hour*24) / float64(winRate)
		}
	}

	// NOTE: there's no need to unlock anything here. Funds only
	// vest on deadline boundaries, and they're unlocked by cron.
	lockedFunds, err := mas.LockedFunds()
	if err != nil {
		return
	}
	availBalance, err := mas.AvailableBalance(mact.Balance)
	if err != nil {
		return
	}
	info.MinerBalance = ConvertBalance(mact.Balance)
	info.PledgedBalance = ConvertPower(lockedFunds.InitialPledgeRequirement)
	info.PreCommitBalance = ConvertBalance(lockedFunds.PreCommitDeposits)
	info.VestingBalance = ConvertBalance(lockedFunds.VestingFunds)
	info.AvailableBalance = ConvertBalance(availBalance)

	wb, err := daemonAPI.WalletBalance(ctx, mi.Worker)
	if err != nil {
		return
	}
	info.WorkerBalance = ConvertBalance(wb)

	if len(mi.ControlAddresses) > 0 {
		var b types.BigInt
		cbsum := types.NewInt(0)
		for _, ca := range mi.ControlAddresses {
			b, err = daemonAPI.WalletBalance(ctx, ca)
			if err != nil {
				return
			}
			cbsum = types.BigAdd(cbsum, b)
		}
		info.ControlBalance = ConvertBalance(cbsum)
	} else {
		info.ControlBalance = 0
	}

	return
}

func ConvertPower(p abi.StoragePower) float64 {
	r := new(big.Rat).SetInt(p.Int)
	f, _ := r.Float64()
	return f
}

func ConvertBalance(bi types.BigInt) float64 {
	r := new(big.Rat).SetFrac(bi.Int, big.NewInt(int64(build.FilecoinPrecision)))
	if r.Sign() == 0 {
		return 0
	}
	f, _ := r.Float64()
	return f
}
