package elins_driver

var (
	ELINS_Init           uintptr
	ELINS_SetRevTime     uintptr
	ELINS_MasterStartSch uintptr
	ELINS_GetMsg         uintptr
	ELINS_MasterStopSch  uintptr
	ELINS_Stop           uintptr
	ELINS_GetStartTime   uintptr
	ELINS_GetSchInfo     uintptr
)

const (
	ELINS_SUCCESS            = -iota ///<函数执行成功
	ELINS_ERR_NOT_SUPPORT            ///<适配器不支持该函数
	ELINS_ERR_USB_WRITE_FAIL         ///<USB写数据失败
	ELINS_ERR_USB_READ_FAIL          ///<USB读数据失败
	ELINS_ERR_CMD_FAIL               ///<命令执行失败
	ELINS_ERR_PARAMETER              ///<参数错误
)
const (
	ELINS_CMD_SDW  = iota + 2 ///<Single Device Write
	ELINS_CMD_SDR             ///<Single Device Read
	ELINS_CMD_BW              ///<Broadcast Write
	ELINS_CMD_EBW             ///<Enhance Broadcast Write
	ELINS_CMD_ESDW            ///<Enhance Single Device Write
	ELINS_CMD_ESDR            ///<Enhance Single Device Read
)
const (
	ELINS_STATUS_NACK  = 1 << iota ///<写无应答(非广播帧)
	ELINS_STATUS_RNRES             ///<读数据无应答
	ELINS_STATUS_ECRC              ///<CRC校验错误
	ELINS_STATUS_FERR              ///<帧异常，实际字节数跟规定的不匹配
	ELINS_STATUS_DLEN              ///<实际数据字节数跟帧头字节数不匹配
	ELINS_STATUS_ECMD              ///<CMD的P0或者P1错误
)

//typedef  struct  _ELINS_MSG
//{
//unsigned char DataLen;          ///<Data域中有效数据字节数
//unsigned char BreakBits;        ///<发送同步间隔宽度，一般为13
//unsigned char Status;           ///<当前帧状态指示，比如帧数据异常可以在这里显示
//unsigned char Flags;            ///<bit[0..1]表示通道号
//unsigned char SYNC;             ///<固定为0x55
//unsigned char TimeStampHigh;    ///<时间戳高位
//unsigned short MsgSendTimes;    ///<当前帧发送次数
//unsigned int  TimeStamp;        ///<接收帧时为时间戳低位，单位为10us，发送数据时为帧间隔时间，单位为微秒(us)
//unsigned char CmdCode;          ///<命令
//unsigned char DevID;            ///<设备ID
//unsigned short RegAddr;         ///<寄存器地址
//unsigned short Crc16;           ///<CRC校验数据，发送时不用填，底层会自动计算，读取时为读到的实际校验数据
//unsigned char Data[64];         ///<数据存储数组，数组里面的有效数据通过DataLen决定
//unsigned char ACKValue[4];      ///<发送需要应答的帧时存储应答数据
//}ELINS_MSG;

type ElinsMsg struct {
	DataLen       byte
	BreakBits     byte
	Status        byte
	Flags         byte
	SYNC          byte
	TimeStampHigh byte
	MsgSendTimes  uint16
	TimeStamp     uint32
	CmdCode       byte
	DevID         byte
	RegAddr       uint16
	Crc16         uint16
	Data          [64]byte
	ACKValue      [4]byte
}

//typedef struct _ELINS_SCH_INFO {
//unsigned int    SchSendTimes;///<调度表发送次数，若为0xFFFFFFFF,表示一直循环发送
//unsigned int    SchSendIndex;///<当前调度表发送次数
//unsigned int    MsgSendIndex;///<当前帧发送次数索引
//unsigned int    AllMsgLen;   ///<调度表里面包含帧数
//unsigned short  MsgIndex;    ///<当前发送帧在调度表里面的索引
//unsigned char   RunFlag;     ///<调度表运行标志
//unsigned char   SaveTxMsg;
//}ELINS_SCH_INFO;

type ElinsSchInfo struct {
	SchSendTimes uint32
	SchSendIndex uint32
	MsgSendIndex uint32
	AllMsgLen    uint32
	MsgIndex     uint16
	RunFlag      byte
	SaveTxMsg    byte
}
