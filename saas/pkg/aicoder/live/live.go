// Package live is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/adapters/live (PASS3 P3-PORT-LIVE), where
// a live file's body is a canonjson.Object instead of map[string]any. The
// names below are aliases of the root package, so a File from either path
// is the same type; old saas callers convert a body at the edge with the
// saas canonjson shell (ToObject / FromObject). New code imports the root
// package directly. The lists below mirror the root package's exported
// names; a name added there is added here while old callers still need it.
package live

import root "github.com/thesatellite-ai/lore/adapters/live"

// Types forwarded from the root package.
type (
	Block           = root.Block
	CompactInputs   = root.CompactInputs
	CompactPlan     = root.CompactPlan
	CompactResult   = root.CompactResult
	ConfirmInputs   = root.ConfirmInputs
	FetchInputs     = root.FetchInputs
	FetchKind       = root.FetchKind
	File            = root.File
	HLC             = root.HLC
	IntegrateAction = root.IntegrateAction
	IntegrateResult = root.IntegrateResult
	LandAction      = root.LandAction
	LandDecision    = root.LandDecision
	LandInputs      = root.LandInputs
	LandItem        = root.LandItem
	LandRequest     = root.LandRequest
	LandResult      = root.LandResult
	LandStatus      = root.LandStatus
	Layout          = root.Layout
	MergeNote       = root.MergeNote
	Meta            = root.Meta
	PendingEntry    = root.PendingEntry
	PushOutcome     = root.PushOutcome
	PushStatus      = root.PushStatus
	Ref             = root.Ref
	RowKey          = root.RowKey
	Snapshot        = root.Snapshot
	Staged          = root.Staged
	StrategySource  = root.StrategySource
	Tree            = root.Tree
	WriteRequest    = root.WriteRequest
	WriteResult     = root.WriteResult
)

// Functions forwarded from the root package.
var (
	BuildLanding    = root.BuildLanding
	Confirm         = root.Confirm
	DecodeFile      = root.DecodeFile
	DefaultMeta     = root.DefaultMeta
	FetchStamp      = root.FetchStamp
	FolderFor       = root.FolderFor
	LandMessage     = root.LandMessage
	MaxHLC          = root.MaxHLC
	MergeTrees      = root.MergeTrees
	NewTombstone    = root.NewTombstone
	NewVersion      = root.NewVersion
	NextHLC         = root.NextHLC
	ParseHLC        = root.ParseHLC
	ParseRowPath    = root.ParseRowPath
	RowPath         = root.RowPath
	SameBody        = root.SameBody
	ShouldCompact   = root.ShouldCompact
	ShouldFetch     = root.ShouldFetch
	ShouldLand      = root.ShouldLand
	StoreRef        = root.StoreRef
	Tombstone       = root.Tombstone
	ValidateFolder  = root.ValidateFolder
	VersionBelow    = root.VersionBelow
	WriteFetchStamp = root.WriteFetchStamp
)

// Variables (errors) forwarded from the root package.
var (
	ErrBelowFloor      = root.ErrBelowFloor
	ErrRemoteRewritten = root.ErrRemoteRewritten
)

// Constants forwarded from the root package.
const (
	BackupsKept          = root.BackupsKept
	BlockKey             = root.BlockKey
	CompactMinInterval   = root.CompactMinInterval
	EpochPath            = root.EpochPath
	FeatureLive          = root.FeatureLive
	FetchAlways          = root.FetchAlways
	FetchCached          = root.FetchCached
	FirstEpoch           = root.FirstEpoch
	IntegrateFastForward = root.IntegrateFastForward
	IntegrateMerged      = root.IntegrateMerged
	IntegrateNothing     = root.IntegrateNothing
	IntegrateReapplied   = root.IntegrateReapplied
	IntegrateTookRemote  = root.IntegrateTookRemote
	LandCopy             = root.LandCopy
	LandDone             = root.LandDone
	LandFinal            = root.LandFinal
	LandNoRemote         = root.LandNoRemote
	LandNothing          = root.LandNothing
	LandOffline          = root.LandOffline
	LandProtected        = root.LandProtected
	LandRefused          = root.LandRefused
	LandRetriesOut       = root.LandRetriesOut
	LandStay             = root.LandStay
	MetaPath             = root.MetaPath
	MinWriterVersion     = root.MinWriterVersion
	PurgedPath           = root.PurgedPath
	PushDone             = root.PushDone
	PushNoRemote         = root.PushNoRemote
	PushNothing          = root.PushNothing
	PushOffline          = root.PushOffline
	PushRefused          = root.PushRefused
	PushRetriesOut       = root.PushRetriesOut
	RefBackupPrefix      = root.RefBackupPrefix
	RefLive              = root.RefLive
	RootFolder           = root.RootFolder
)
