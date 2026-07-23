package driver

// TSMaster
const (
	TS_UNKNOWN_DEVICE = iota
	TSCAN_PRO
	TSCAN_Lite1
	TC1001
	TL1001
	TC1011
	TM5011
	TC1002
	TC1014
	TSCANFD2517
	TC1026
	TC1016
	TC1012
	TC1013
	TLog1002
	TC1034
	TC1018
	GW2116
	TC2115
	MP1013
	TC1113
	TC1114
	TP1013
	TC1017
	TP1018
	TF10XX
	TL1004_FD_4_LIN_2
	TE1051
	TP1051
	TP1034
	TTS9015
	TP1026
	TTS1026
	TTS1034
	TTS1018
	TL1011
	TTS1015_LiAuto
	TTS1013_LiAuto
	TTS1016Pro
	TC1054Pro
	TC1054
	TLog1038
	TO1013
	TC1034Pro
	TC1018Pro
	TC1038Pro
	TC1014Pro
	TC1034ProPlus
	TA1038
	TC1055Pro
	TC1056Pro
	TC1057Pro
	TC4016
	GW2208
	TLog1039
	GW1040
	TC3014
	TP1014
	TA825_4
	TC1013HV
	TC1052
	TTS1017Pro
	TLog1057
	TC1017Pro
	GW2202
	GW2204
	GW2212
	TA821
	TX1000
	TC1055ProPlus
	TC1043
	TS_DEV_END
)

// TSMasterMap 设备编号对照表
var TSMasterMap = map[string]int{
	"TS_UNKNOWN_DEVICE": 0,
	"TSCAN_PRO":         1,
	"TSCAN_Lite1":       2,
	"TC1001":            3,
	"TL1001":            4,
	"TC1011":            5,
	"TM5011":            6,
	"TC1002":            7,
	"TC1014":            8,
	"TSCANFD2517":       9,
	"TC1026":            10,
	"TC1016":            11,
	"TC1012":            12,
	"TC1013":            13,
	"TLog1002":          14,
	"TC1034":            15,
	"TC1018":            16,
	"GW2116":            17,
	"TC2115":            18,
	"MP1013":            19,
	"TC1113":            20,
	"TC1114":            21,
	"TP1013":            22,
	"TC1017":            23,
	"TP1018":            24,
	"TF10XX":            25,
	"TL1004_FD_4_LIN_2": 26,
	"TE1051":            27,
	"TP1051":            28,
	"TP1034":            29,
	"TTS9015":           30,
	"TP1026":            31,
	"TTS1026":           32,
	"TTS1034":           33,
	"TTS1018":           34,
	"TL1011":            35,
	"TTS1015_LiAuto":    36,
	"TTS1013_LiAuto":    37,
	"TTS1016Pro":        38,
	"TC1054Pro":         39,
	"TC1054":            40,
	"TLog1038":          41,
	"TO1013":            42,
	"TC1034Pro":         43,
	"TC1018Pro":         44,
	"TC1038Pro":         45,
	"TC1014Pro":         46,
	"TC1034ProPlus":     47,
	"TA1038":            48,
	"TC1055Pro":         49,
	"TC1056Pro":         50,
	"TC1057Pro":         51,
	"TC4016":            52,
	"GW2208":            53,
	"TLog1039":          54,
	"GW1040":            55,
	"TC3014":            56,
	"TP1014":            57,
	"TA825_4":           58,
	"TC1013HV":          59,
	"TC1052":            60,
	"TTS1017Pro":        61,
	"TLog1057":          62,
	"TC1017Pro":         63,
	"GW2202":            64,
	"GW2204":            65,
	"GW2212":            66,
	"TA821":             67,
	"TX1000":            68,
	"TC1055ProPlus":     69,
	"TC1043":            70,
	"TS_DEV_END":        71,
}
