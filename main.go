package alyx

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Monibuca/engine/v2/util"

	. "github.com/Monibuca/engine/v2"
	"github.com/Monibuca/engine/v2/avformat"
	. "github.com/Monibuca/plugin-rtp"
	. "github.com/logrusorgru/aurora"
	. "github.com/pion/webrtc/v2"
	"github.com/pion/webrtc/v2/pkg/media"
)

var config struct {
	ICEServers []string
}

func init() {
	InstallPlugin(&PluginConfig{
		Config: &config,
		Name:   "Alyx",
		Type:   PLUGIN_PUBLISHER | PLUGIN_SUBSCRIBER,
		Run:    run,
	})
}

type WebRTC struct {
	RTP
	*PeerConnection
	RemoteAddr string
	m          MediaEngine
	api        *API
	payloader  AH264
	myAlyxMap  sync.Map
	// codecs.H264Packet
	// *os.File
}
type myAlyx struct {
	Subscriber
	*Track
}

var lastTimeStamp uint32

func (rtc *WebRTC) Play(streamPath string) bool {
	Print(Sprintf(Yellow("into Play function")))

	rtc.OnICEConnectionStateChange(func(connectionState ICEConnectionState) {
		Printf("%s Connection State has changed %s ", streamPath, connectionState.String())
		switch connectionState {
		case ICEConnectionStateDisconnected:
			rtc.myAlyxMap.Range(func(key, value interface{}) bool {
				tmpMyAlyx := value.(*myAlyx)
				tmpMyAlyx.Subscriber.Close()
				return true
			})
		case ICEConnectionStateConnected:
			rtc.myAlyxMap.Range(func(key, value interface{}) bool {
				tmpMyAlyx := value.(*myAlyx)
				tmpMyAlyx.Subscriber.OnData = func(packet *avformat.SendPacket) error {
					if packet.Type == avformat.FLV_TAG_TYPE_AUDIO {
						return nil
					}
					if packet.IsSequence {
						return nil
					}
					var s uint32
					if lastTimeStamp > 0 {
						s = packet.Timestamp - lastTimeStamp
					}
					lastTimeStamp = packet.Timestamp
					if packet.IsKeyFrame {
						var l uint32 = uint32(len(tmpMyAlyx.Subscriber.SPS))
						spsLen := make([]byte, 4)
						util.BigEndian.PutUint32(spsLen, l)

						l = uint32(len(tmpMyAlyx.Subscriber.PPS))
						ppsLen := make([]byte, 4)
						util.BigEndian.PutUint32(ppsLen, l)

						tmpMyAlyx.Track.WriteSample(media.Sample{
							Data:    append(spsLen, tmpMyAlyx.Subscriber.SPS...),
							Samples: 0,
						})
						tmpMyAlyx.Track.WriteSample(media.Sample{
							Data:    append(ppsLen, tmpMyAlyx.Subscriber.PPS...),
							Samples: 0,
						})
					}
					tmpMyAlyx.Track.WriteSample(media.Sample{
						Data:    packet.Payload[5:],
						Samples: s * 90,
					})
					return nil
				}
				tmpMyAlyx.Subscribe(key.(string))
				return true
			})
			//rtc.videoTrack = rtc.GetSenders()[0].Track()
		}
	})
	return true
}
func (rtc *WebRTC) GetAnswer() ([]byte, error) {
	// Sets the LocalDescription, and starts our UDP listeners
	answer, err := rtc.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	if err := rtc.SetLocalDescription(answer); err != nil {
		Println(err)
		return nil, err
	}
	if bytes, err := json.Marshal(answer); err != nil {
		Println(err)
		return bytes, err
	} else {
		return bytes, nil
	}
}

type JsonResult struct {
	Code int         `json:"code"`
	Data interface{} `json:"data"`
}
type StreamPathsJson struct {
	StreamPaths string `json:"streamPaths"`
}

func run() {
	Print(Sprintf(Yellow("alxy is running ")))
	http.HandleFunc("/alyx/queryList", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		var jsonresult JsonResult
		jsonresult.Code = 0
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if len(Summary.Streams) > 0 {
			var tmpStrings []string
			for _, stream := range Summary.Streams {
				tmpStrings = append(tmpStrings, stream.StreamPath)
			}
			jsonresult.Data = tmpStrings
		} else {
			jsonresult.Data = []string{}
		}
		bytes, err := json.Marshal(jsonresult)
		if err == nil {
			w.Write(bytes)
		} else {
			return
		}
	})
	http.HandleFunc("/alyx/play", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		streamPaths := strings.Split(r.URL.Query().Get("streamPath"), ",")
		var offer SessionDescription
		bytes, err := ioutil.ReadAll(r.Body)
		fmt.Printf("streamPaths is %#v\n", streamPaths)
		var rtc WebRTC
		if err = json.Unmarshal(bytes, &offer); err != nil {
			return
		}
		defer func() {
			if err != nil {
				Println(err)
				fmt.Fprintf(w, `{"errmsg":"%s"}`, err)
				return
			}
			rtc.Play("streamPath")
		}()
		//if err != nil {
		//	return
		//}
		pli := "42001f"
		//if stream := FindStream(streamPath); stream != nil {
		//	pli = fmt.Sprintf("%x", stream.SPS[1:4])
		//}
		rtc.m.RegisterCodec(NewRTPCodec(RTPCodecTypeVideo,
			H264,
			90000,
			0,
			"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id="+pli[:2]+"001f",
			DefaultPayloadTypeH264,
			&rtc.payloader))
		//m.RegisterCodec(NewRTPPCMUCodec(DefaultPayloadTypePCMU, 8000))
		rtc.api = NewAPI(WithMediaEngine(rtc.m))
		peerConnection, err := rtc.api.NewPeerConnection(Configuration{
			// ICEServers: []ICEServer{
			// 	{
			// 		URLs: config.ICEServers,
			// 	},
			// },
		})
		rtc.PeerConnection = peerConnection
		rtc.OnICECandidate(func(ice *ICECandidate) {
			if ice != nil {
				Println(ice.ToJSON().Candidate)
			}
		})
		// if r, err := peerConnection.AddTransceiverFromKind(RTPCodecTypeVideo); err == nil {
		// 	rtc.videoTrack = r.Sender().Track()
		// } else {
		// 	Println(err)
		// }
		if err != nil {
			return
		}
		rtc.RemoteAddr = r.RemoteAddr
		if err = rtc.SetRemoteDescription(offer); err != nil {
			return
		}
		// rtc.m.PopulateFromSDP(offer)
		// var vpayloadType uint8 = 0

		// for _, videoCodec := range rtc.m.GetCodecsByKind(RTPCodecTypeVideo) {
		// 	if videoCodec.Name == H264 {
		// 		vpayloadType = videoCodec.PayloadType
		// 		videoCodec.Payloader = &rtc.payloader
		// 		Printf("H264 fmtp %v", videoCodec.SDPFmtpLine)

		// 	}
		// }
		// println(vpayloadType)

		for i, v := range streamPaths {
			var tmpVideoTrack *Track
			if tmpVideoTrack, err = rtc.NewTrack(DefaultPayloadTypeH264, uint32(i+1), "video", v); err != nil {
				Print(err)
				continue
			}
			tmp := myAlyx{Track: tmpVideoTrack}
			tmp.Subscriber.ID = rtc.RemoteAddr
			tmp.Type = "Alyx"
			rtc.AddTrack(tmpVideoTrack)
			rtc.myAlyxMap.Store(v, &tmp)
		}
		//
		//if _, err = rtc.AddTrack(rtc.videoTrack); err != nil {
		//	return
		//}
		if bytes, err := rtc.GetAnswer(); err == nil {
			w.Write(bytes)
		} else {
			return
		}
	})
}

var recordings sync.Map

func record() error {
	streamList := Summary.Streams
	for _, v := range streamList {
		tmpStreamPath := v.StreamPath

		var filePath string
		var file *os.File
		var err error
		p := Subscriber{OnData: func(packet *avformat.SendPacket) error {
			timeout := time.After(time.Minute)
			tmpStream := GetStream(tmpStreamPath)
			first := true
			for {
				select {
				case <-timeout:
					file.Close()
					first = false
				default:
					filePath := filepath.Join(tmpStreamPath+"/", time.Now().Format("20060102150400")+".flv")
					os.MkdirAll(path.Dir(filePath), 0775)
					file, err := os.OpenFile(filePath, os.O_CREATE, 0775)
					if err != nil {
						return err
					}
					if !first {
						_, err = file.Write(avformat.FLVHeader)
						tmpVideoTag := &avformat.SendPacket{
							AVPacket:  tmpStream.VideoTag,
							Timestamp: packet.Timestamp,
						}
						avformat.WriteFLVTag(file, tmpVideoTag)
						tmpAideoTag := &avformat.SendPacket{
							AVPacket:  tmpStream.AudioTag,
							Timestamp: packet.Timestamp,
						}
						avformat.WriteFLVTag(file, tmpAideoTag)
					}
					avformat.WriteFLVTag(file, packet)
				}
			}
		}}
		p.ID = filePath
		p.Type = "FlvRecord"
		if err == nil {
			recordings.Store(filePath, &p)
			go func() {
				p.Subscribe(tmpStreamPath)
				file.Close()
			}()
		} else {
			file.Close()
		}
		return err
	}
	return nil
}
