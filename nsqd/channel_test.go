package nsqd

import (
	//"github.com/youzan/nsq/internal/levellogger"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	simpleJson "github.com/bitly/go-simplejson"
	"github.com/youzan/nsq/internal/ext"
)

type fakeConsumer struct {
	cid int64
}

func NewFakeConsumer(id int64) *fakeConsumer {
	return &fakeConsumer{cid: id}
}

func (c *fakeConsumer) UnPause() {
}
func (c *fakeConsumer) Pause() {
}
func (c *fakeConsumer) TimedOutMessage() {
}
func (c *fakeConsumer) RequeuedMessage() {
}
func (c *fakeConsumer) FinishedMessage() {
}
func (c *fakeConsumer) Stats() ClientStats {
	return ClientStats{}
}
func (c *fakeConsumer) Exit() {
}
func (c *fakeConsumer) Empty() {
}
func (c *fakeConsumer) String() string {
	return ""
}
func (c *fakeConsumer) GetID() int64 {
	return c.cid
}

func (c *fakeConsumer) SkipZanTest() {

}

func (c *fakeConsumer) UnskipZanTest() {

}

// ensure that we can push a message through a topic and get it out of a channel
func TestPutMessage(t *testing.T) {
	opts := NewOptions()
	opts.Logger = newTestLogger(t)
	opts.LogLevel = 3
	opts.SyncEvery = 1
	if testing.Verbose() {
		opts.LogLevel = 4
		SetLogger(opts.Logger)
	}
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_put_message" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel1 := topic.GetChannel("ch")

	var id MessageID
	msg := NewMessage(id, []byte("test"))
	topic.PutMessage(msg)
	topic.ForceFlush()

	select {
	case outputMsg := <-channel1.clientMsgChan:
		equal(t, msg.ID, outputMsg.ID)
		equal(t, msg.Body, outputMsg.Body)
	case <-time.After(time.Second * 10):
		t.Logf("timeout wait")
	}
}

// ensure that both channels get the same message
func TestPutMessage2Chan(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	opts.LogLevel = 3
	SetLogger(opts.Logger)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_put_message_2chan" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel1 := topic.GetChannel("ch1")
	channel2 := topic.GetChannel("ch2")

	var id MessageID
	msg := NewMessage(id, []byte("test"))
	topic.PutMessage(msg)
	topic.flushBuffer(true)

	outputMsg1 := <-channel1.clientMsgChan
	equal(t, msg.ID, outputMsg1.ID)
	equal(t, msg.Body, outputMsg1.Body)

	outputMsg2 := <-channel2.clientMsgChan
	equal(t, msg.ID, outputMsg2.ID)
	equal(t, msg.Body, outputMsg2.Body)
}

func TestChannelBackendMaxMsgSize(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_backend_maxmsgsize" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)

	equal(t, topic.backend.maxMsgSize, int32(opts.MaxMsgSize+minValidMsgLength))
}

func TestInFlightWorker(t *testing.T) {
	count := 250

	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	opts.MsgTimeout = 100 * time.Millisecond
	opts.QueueScanRefreshInterval = 100 * time.Millisecond
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_in_flight_worker" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	for i := 0; i < count; i++ {
		msg := NewMessage(topic.nextMsgID(), []byte("test"))
		channel.StartInFlightTimeout(msg, NewFakeConsumer(0), "", opts.MsgTimeout)
	}

	channel.Lock()
	inFlightMsgs := len(channel.inFlightMessages)
	channel.Unlock()
	equal(t, inFlightMsgs, count)

	channel.inFlightMutex.Lock()
	inFlightPQMsgs := len(channel.inFlightPQ)
	channel.inFlightMutex.Unlock()
	equal(t, inFlightPQMsgs, count)

	// the in flight worker has a resolution of 100ms so we need to wait
	// at least that much longer than our msgTimeout (in worst case)
	time.Sleep(4*opts.MsgTimeout + opts.QueueScanInterval)

	channel.Lock()
	inFlightMsgs = len(channel.inFlightMessages)
	channel.Unlock()
	equal(t, inFlightMsgs, 0)

	channel.inFlightMutex.Lock()
	inFlightPQMsgs = len(channel.inFlightPQ)
	channel.inFlightMutex.Unlock()
	equal(t, inFlightPQMsgs, 0)
}

func TestChannelEmpty(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_empty" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	msgs := make([]*Message, 0, 25)
	for i := 0; i < 25; i++ {
		msg := NewMessage(topic.nextMsgID(), []byte("test"))
		channel.StartInFlightTimeout(msg, NewFakeConsumer(0), "", opts.MsgTimeout)
		msgs = append(msgs, msg)
	}

	channel.RequeueMessage(0, "", msgs[len(msgs)-1].ID, 0, true)
	equal(t, len(channel.inFlightMessages), 24)
	equal(t, len(channel.inFlightPQ), 24)

	channel.skipChannelToEnd()

	equal(t, len(channel.inFlightMessages), 0)
	equal(t, len(channel.inFlightPQ), 0)
	equal(t, channel.Depth(), int64(0))
}

func TestChannelEmptyWhileConfirmDelayMsg(t *testing.T) {
	// test confirm delay counter while empty channel
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	opts.QueueScanInterval = time.Millisecond
	opts.MsgTimeout = time.Second
	if testing.Verbose() {
		opts.LogLevel = 2
		SetLogger(opts.Logger)
	}
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_empty_delay" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")
	dq, err := topic.GetOrCreateDelayedQueueNoLock(nil)
	equal(t, err, nil)
	stopC := make(chan bool)
	for i := 0; i < 100; i++ {
		msg := NewMessage(0, []byte("test"))
		id, _, _, _, err := topic.PutMessage(msg)
		equal(t, err, nil)
		newMsg := msg.GetCopy()
		newMsg.ID = 0
		newMsg.DelayedType = ChannelDelayed

		newTimeout := time.Now().Add(time.Millisecond)
		newMsg.DelayedTs = newTimeout.UnixNano()

		newMsg.DelayedOrigID = id
		newMsg.DelayedChannel = channel.GetName()

		_, _, _, _, err = dq.PutDelayMessage(newMsg)
		equal(t, err, nil)
	}

	channel.skipChannelToEnd()
	channel.AddClient(1, NewFakeConsumer(1))
	go func() {
		for {
			select {
			case <-stopC:
				return
			default:
			}
			outputMsg, ok := <-channel.clientMsgChan

			if !ok {
				return
			}
			t.Logf("consume %v", outputMsg)
			channel.StartInFlightTimeout(outputMsg, NewFakeConsumer(0), "", opts.MsgTimeout)
			channel.FinishMessageForce(0, "", outputMsg.ID, true)
			time.Sleep(time.Millisecond)
		}
	}()

	go func() {
		for {
			select {
			case <-stopC:
				return
			default:
			}
			channel.skipChannelToEnd()
			_, dqCnt := channel.GetDelayedQueueConsumedState()
			if int64(dqCnt) == 0 && atomic.LoadInt64(&channel.deferredFromDelay) == 0 && channel.Depth() == 0 {
				close(stopC)
				return
			}
			time.Sleep(time.Millisecond * 10)
		}
	}()

	go func() {
		for {
			select {
			case <-stopC:
				return
			default:
			}
			checkOK := atomic.LoadInt64(&channel.deferredFromDelay) >= int64(0)
			equal(t, checkOK, true)
			if !checkOK {
				close(stopC)
				return
			}
			time.Sleep(time.Microsecond * 10)
		}
	}()
	done := false
	for done {
		select {
		case <-stopC:
			done = true
		case <-time.After(time.Second * 3):
			_, dqCnt := channel.GetDelayedQueueConsumedState()
			if int64(dqCnt) == 0 && atomic.LoadInt64(&channel.deferredFromDelay) == 0 && channel.Depth() == 0 {
				close(stopC)
				done = true
			}
		}
	}
	t.Logf("stopped %v, %v", atomic.LoadInt64(&channel.deferredFromDelay), channel.GetChannelDebugStats())
	time.Sleep(time.Second * 3)
	// make sure all delayed counter is not more or less
	equal(t, atomic.LoadInt64(&channel.deferredFromDelay) == int64(0), true)
}

func TestChannelHealth(t *testing.T) {
	opts := NewOptions()
	opts.Logger = newTestLogger(t)
	opts.MemQueueSize = 2

	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topic := nsqd.GetTopicIgnPart("test")

	channel := topic.GetChannel("channel")
	// cause channel.messagePump to exit so we can set channel.backend without
	// a data race. side effect is it closes clientMsgChan, and messagePump is
	// never restarted. note this isn't the intended usage of exitChan but gets
	// around the data race without more invasive changes to how channel.backend
	// is set/loaded.
	channel.exitChan <- 1
}

func TestChannelSkip(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_skip" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	msgs := make([]*Message, 0, 10)
	for i := 0; i < 10; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)

	var msgId MessageID
	msgBytes := []byte(strconv.Itoa(10))
	msg := NewMessage(msgId, msgBytes)
	_, backendOffsetMid, _, _, _ := topic.PutMessage(msg)
	topic.ForceFlush()
	equal(t, channel.Depth(), int64(11))

	msgs = make([]*Message, 0, 9)
	//put another 10 messages
	for i := 0; i < 9; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i + 11))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)
	time.Sleep(time.Millisecond)
	equal(t, channel.Depth(), int64(20))

	//skip forward to message 10
	t.Logf("backendOffsetMid: %d", backendOffsetMid)
	channel.SetConsumeOffset(backendOffsetMid, 10, true)
	time.Sleep(time.Second)
	for i := 0; i < 10; i++ {
		outputMsg := <-channel.clientMsgChan
		equal(t, string(outputMsg.Body[:]), strconv.Itoa(i+10))
	}
}

func TestChannelSkipZanTestForOrdered(t *testing.T) {
	// while the ordered message is timeouted and requeued,
	// change the state to skip zan test may block waiting the next
	// we test this case for ordered topic
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	opts.MsgTimeout = time.Second * 2
	opts.AllowZanTestSkip = true
	if testing.Verbose() {
		opts.LogLevel = 4
		SetLogger(opts.Logger)
	}
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_skiptest_order" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicWithExt(topicName, 0, true)
	channel := topic.GetChannel("order_channel")
	channel.SetOrdered(true)
	channel.doSkipZanTest(false)

	msgs := make([]*Message, 0, 3)
	for i := 0; i < 3; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)

	msgs = make([]*Message, 0, 3)
	//put another 10 zan test messages
	for i := 0; i < 3; i++ {
		var msgId MessageID
		msgBytes := []byte("zan_test")
		extJ := simpleJson.New()
		extJ.Set(ext.ZAN_TEST_KEY, true)
		extBytes, _ := extJ.Encode()
		msg := NewMessageWithExt(msgId, msgBytes, ext.JSON_HEADER_EXT_VER, extBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)
	time.Sleep(time.Millisecond)

	msgs = make([]*Message, 0, 3)
	// put another normal messsages
	for i := 0; i < 3; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i + 11 + 10))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)
	time.Sleep(time.Second)
	// consume normal message and some test message
	for i := 0; i < 3; i++ {
		outputMsg := <-channel.clientMsgChan
		t.Logf("consume %v", string(outputMsg.Body))
		channel.StartInFlightTimeout(outputMsg, NewFakeConsumer(0), "", opts.MsgTimeout)
		channel.FinishMessageForce(0, "", outputMsg.ID, true)
		channel.ContinueConsumeForOrder()
	}
	time.Sleep(time.Millisecond * 10)
	// make sure zan test timeout
	outputMsg := <-channel.clientMsgChan
	t.Logf("consume %v", string(outputMsg.Body))
	channel.StartInFlightTimeout(outputMsg, NewFakeConsumer(0), "", opts.MsgTimeout)
	equal(t, []byte("zan_test"), outputMsg.Body)
	time.Sleep(time.Millisecond * 10)
	// skip zan test soon to make sure the zan test is inflight
	channel.doSkipZanTest(true)
	time.Sleep(time.Second * 3)
	toC := time.After(time.Second * 30)

	// set zan test skip and should continue consume normal messages
	for i := 0; i < 3; i++ {
		select {
		case outputMsg = <-channel.clientMsgChan:
		case <-toC:
			t.Errorf("timeout waiting consume")
			return
		}
		t.Logf("consume %v, %v, %v", string(outputMsg.Body), string(outputMsg.ExtBytes), outputMsg.ExtVer)
		channel.StartInFlightTimeout(outputMsg, NewFakeConsumer(0), "", opts.MsgTimeout)
		channel.FinishMessageForce(0, "", outputMsg.ID, true)
		channel.ContinueConsumeForOrder()
		nequal(t, []byte("zan_test"), outputMsg.Body)
		if channel.Depth() == 0 {
			break
		}
	}
	equal(t, channel.Depth(), int64(0))
}

func TestChannelInitWithOldStart(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.MaxBytesPerFile = 1024
	opts.LogLevel = 4
	opts.Logger = newTestLogger(t)
	if testing.Verbose() {
		opts.LogLevel = 4
		SetLogger(opts.Logger)
	}
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_init_oldstart" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")
	channel2 := topic.GetChannel("channel2")
	channel3 := topic.GetChannel("channel3")

	msgs := make([]*Message, 0, 10)
	for i := 0; i < 10; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)

	var msgId MessageID
	msgBytes := []byte(strconv.Itoa(10))
	msg := NewMessage(msgId, msgBytes)
	_, backendOffsetMid, _, _, _ := topic.PutMessage(msg)
	topic.ForceFlush()
	equal(t, channel.Depth(), int64(11))
	channel.SetConsumeOffset(backendOffsetMid, 10, true)
	time.Sleep(time.Second)
	outputMsg := <-channel.clientMsgChan
	equal(t, string(outputMsg.Body[:]), strconv.Itoa(10))
	topic.CloseExistingChannel("channel", false)
	time.Sleep(time.Second)

	msgs = make([]*Message, 0, 1000-10)
	for i := 10; i < 1000; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	_, putOffset, _, _, putEnd, _ := topic.PutMessages(msgs)
	topic.ForceFlush()
	t.Log(putEnd)
	t.Log(putOffset)

	var msgId2 MessageID
	msgBytes = []byte(strconv.Itoa(1001))
	msg = NewMessage(msgId2, msgBytes)
	topic.PutMessage(msg)
	topic.ForceFlush()

	channel2.skipChannelToEnd()
	channel3.SetConsumeOffset(putEnd.Offset(), putEnd.TotalMsgCnt(), true)
	topic.CloseExistingChannel(channel3.GetName(), false)
	topic.TryCleanOldData(1024*2, false, topic.backend.GetQueueReadEnd().Offset())
	t.Log(topic.GetQueueReadStart())
	t.Log(topic.backend.GetQueueReadEnd())

	// closed channel reopen should check if old confirmed is not less than cleaned disk segment start
	// and the reopened channel read end can update to the newest topic end
	channel = topic.GetChannel("channel")
	t.Log(channel.GetConfirmed())
	t.Log(channel.GetChannelEnd())

	equal(t, channel.GetConfirmed(), topic.backend.GetQueueReadStart())
	equal(t, channel.GetChannelEnd(), topic.backend.GetQueueReadEnd())
	channel3 = topic.GetChannel(channel3.GetName())

	t.Log(channel3.GetConfirmed())
	t.Log(channel3.GetChannelEnd())

	equal(t, channel3.GetConfirmed(), putEnd)
	equal(t, channel3.GetChannelEnd(), topic.backend.GetQueueReadEnd())
}

func TestChannelResetReadEnd(t *testing.T) {
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_skip" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	msgs := make([]*Message, 0, 10)
	for i := 0; i < 10; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)

	var msgId MessageID
	msgBytes := []byte(strconv.Itoa(10))
	msg := NewMessage(msgId, msgBytes)
	_, backendOffsetMid, _, _, _ := topic.PutMessage(msg)
	topic.ForceFlush()
	equal(t, channel.Depth(), int64(11))

	msgs = make([]*Message, 0, 9)
	//put another 10 messages
	for i := 0; i < 9; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i + 11))
		msg := NewMessage(msgId, msgBytes)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)
	time.Sleep(time.Millisecond)
	equal(t, channel.Depth(), int64(20))

	//skip forward to message 10
	t.Logf("backendOffsetMid: %d", backendOffsetMid)
	channel.SetConsumeOffset(backendOffsetMid, 10, true)
	time.Sleep(time.Millisecond)
	for i := 0; i < 10; i++ {
		outputMsg := <-channel.clientMsgChan
		t.Logf("Msg: %s", outputMsg.Body)
		equal(t, string(outputMsg.Body[:]), strconv.Itoa(i+10))
	}
	equal(t, channel.Depth(), int64(10))

	channel.SetConsumeOffset(0, 0, true)
	time.Sleep(time.Millisecond)
	//equal(t, channel.Depth(), int64(20))
	for i := 0; i < 20; i++ {
		outputMsg := <-channel.clientMsgChan
		t.Logf("Msg: %s", outputMsg.Body)
		equal(t, string(outputMsg.Body[:]), strconv.Itoa(i))
	}
}

// depth timestamp is the next msg time need to be consumed
func TestChannelDepthTimestamp(t *testing.T) {
	// handle read no data, reset, etc
	opts := NewOptions()
	opts.SyncEvery = 1
	opts.Logger = newTestLogger(t)
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_depthts" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	msgs := make([]*Message, 0, 9)
	//put another 10 messages
	for i := 0; i < 10; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i + 11))
		msg := NewMessage(msgId, msgBytes)
		time.Sleep(time.Millisecond * 10)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.ForceFlush()

	lastDepthTs := int64(0)
	for i := 0; i < 9; i++ {
		msgOutput := <-channel.clientMsgChan
		time.Sleep(time.Millisecond * 10)
		if lastDepthTs != 0 {
			// next msg timestamp == last depth ts
			equal(t, msgOutput.Timestamp, lastDepthTs)
		}
		lastDepthTs = channel.DepthTimestamp()
	}
	channel.resetReaderToConfirmed()
	equal(t, channel.DepthTimestamp(), int64(0))
}

func TestChannelUpdateEndWhenNeed(t *testing.T) {
	// put will try update channel end if channel need more data
	// and channel will try get newest end while need more data (no new put)
	// consider below:
	// 1. put 1,2,3 update channel end to 3
	// 2. consume 1
	// 3. put 4, no need update end
	// 4. consume 2, 3
	// 5. check consume 4 without topic flush
	// 6. consume end and put 5
	// 7. check consume 5 without flush
	opts := NewOptions()
	opts.SyncEvery = 100
	opts.LogLevel = 2
	opts.SyncTimeout = time.Second * 10
	opts.Logger = newTestLogger(t)
	if testing.Verbose() {
		opts.LogLevel = 4
		SetLogger(opts.Logger)
	}
	_, _, nsqd := mustStartNSQD(opts)
	defer os.RemoveAll(opts.DataPath)
	defer nsqd.Exit()

	topicName := "test_channel_end_update" + strconv.Itoa(int(time.Now().Unix()))
	topic := nsqd.GetTopicIgnPart(topicName)
	channel := topic.GetChannel("channel")

	msgs := make([]*Message, 0, 9)
	for i := 0; i < 10; i++ {
		var msgId MessageID
		msgBytes := []byte(strconv.Itoa(i + 11))
		msg := NewMessage(msgId, msgBytes)
		time.Sleep(time.Millisecond)
		msgs = append(msgs, msg)
	}
	topic.PutMessages(msgs)
	topic.flushBuffer(true)

	for i := 0; i < 5; i++ {
		msgOutput := <-channel.clientMsgChan
		channel.StartInFlightTimeout(msgOutput, NewFakeConsumer(0), "", opts.MsgTimeout)
		channel.ConfirmBackendQueue(msgOutput)
		t.Logf("consume %v", string(msgOutput.Body))
	}
	for i := 0; i < 5; i++ {
		var id MessageID
		msg := NewMessage(id, []byte("test"))
		topic.PutMessage(msg)
	}
	for i := 0; i < 10; i++ {
		select {
		case msgOutput := <-channel.clientMsgChan:
			channel.StartInFlightTimeout(msgOutput, NewFakeConsumer(0), "", opts.MsgTimeout)
			channel.ConfirmBackendQueue(msgOutput)
			t.Logf("consume %v", string(msgOutput.Body))
		case <-time.After(time.Second):
			t.Fatalf("timeout consume new messages")
		}
	}
	for i := 0; i < 5; i++ {
		var id MessageID
		msg := NewMessage(id, []byte("test"))
		topic.PutMessage(msg)
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		select {
		case msgOutput := <-channel.clientMsgChan:
			channel.StartInFlightTimeout(msgOutput, NewFakeConsumer(0), "", opts.MsgTimeout)
			channel.ConfirmBackendQueue(msgOutput)
			t.Logf("consume %v", string(msgOutput.Body))
		case <-time.After(time.Second):
			t.Fatalf("timeout consume new messages")
		}
	}
	// test new conn consume start from end queue before new message puts
}

func TestRangeTree(t *testing.T) {
	//tr := NewIntervalTree()
	tr := NewIntervalSkipList()
	//tr := NewIntervalHash()
	m1 := &queueInterval{0, 10, 2}
	m2 := &queueInterval{10, 20, 3}
	m3 := &queueInterval{20, 30, 4}
	m4 := &queueInterval{30, 40, 5}
	m5 := &queueInterval{40, 50, 6}
	m6 := &queueInterval{50, 60, 7}

	ret := tr.Query(m1, false)
	equal(t, len(ret), 0)
	equal(t, m1, tr.AddOrMerge(m1))
	t.Logf("1 %v", tr.ToString())

	ret = tr.Query(m1, false)
	equal(t, len(ret), 1)
	lowest := tr.IsLowestAt(m1.Start())
	equal(t, lowest, m1)
	lowest = tr.IsLowestAt(m1.End())
	equal(t, lowest, nil)
	deleted := tr.DeleteLower(m1.Start() + (m1.End()-m1.Start())/2)
	equal(t, deleted, 0)
	ret = tr.Query(m3, false)
	equal(t, len(ret), 0)
	ret = tr.Query(m3, true)
	equal(t, len(ret), 0)
	ret = tr.Query(m2, true)
	equal(t, len(ret), 0)
	ret = tr.Query(m2, false)
	equal(t, len(ret), 1)
	lowest = tr.IsLowestAt(m1.Start())
	equal(t, lowest, m1)
	tr.AddOrMerge(m3)
	t.Logf("2 %v", tr.ToString())
	ret = tr.Query(m5, false)
	equal(t, len(ret), 0)
	ret = tr.Query(m2, false)
	equal(t, len(ret), 2)
	ret = tr.Query(m4, false)
	equal(t, len(ret), 1)
	tr.AddOrMerge(m5)
	ret = tr.Query(m2, false)
	equal(t, len(ret), 2)
	ret = tr.Query(m4, false)
	equal(t, len(ret), 2)
	ret = tr.Query(m4, true)
	equal(t, len(ret), 0)
	lowest = tr.IsLowestAt(m1.Start())
	equal(t, lowest, m1)
	lowest = tr.IsLowestAt(m3.Start())
	equal(t, lowest, nil)

	deleted = tr.DeleteLower(m1.Start() + (m1.End()-m1.Start())/2)
	equal(t, deleted, 0)

	merged := tr.AddOrMerge(m2)
	t.Logf("4 %v", tr.ToString())
	equal(t, merged.Start(), m1.Start())
	equal(t, merged.End(), m3.End())
	equal(t, merged.EndCnt(), m3.EndCnt())
	equal(t, true, tr.IsCompleteOverlap(m1))
	equal(t, true, tr.IsCompleteOverlap(m2))
	equal(t, true, tr.IsCompleteOverlap(m3))

	ret = tr.Query(m6, false)
	equal(t, len(ret), 1)

	merged = tr.AddOrMerge(m6)
	equal(t, merged.Start(), m5.Start())
	equal(t, merged.End(), m6.End())
	equal(t, merged.EndCnt(), m6.EndCnt())
	equal(t, true, tr.IsCompleteOverlap(m5))
	equal(t, true, tr.IsCompleteOverlap(m6))

	ret = tr.Query(m4, false)
	equal(t, len(ret), 2)

	merged = tr.AddOrMerge(m4)

	equal(t, tr.Len(), int(1))
	equal(t, merged.Start(), int64(0))
	equal(t, merged.End(), int64(60))
	equal(t, merged.EndCnt(), uint64(7))
	equal(t, true, tr.IsCompleteOverlap(m1))
	equal(t, true, tr.IsCompleteOverlap(m2))
	equal(t, true, tr.IsCompleteOverlap(m3))
	equal(t, true, tr.IsCompleteOverlap(m4))
	equal(t, true, tr.IsCompleteOverlap(m5))
	equal(t, true, tr.IsCompleteOverlap(m6))
	merged = tr.AddOrMerge(m1)
	equal(t, merged.Start(), int64(0))
	equal(t, merged.End(), int64(60))
	equal(t, merged.EndCnt(), uint64(7))
	merged = tr.AddOrMerge(m6)
	equal(t, merged.Start(), int64(0))
	equal(t, merged.End(), int64(60))
	equal(t, merged.EndCnt(), uint64(7))

	deleted = tr.DeleteLower(m1.Start() + (m1.End()-m1.Start())/2)
	equal(t, deleted, 0)
	deleted = tr.DeleteLower(int64(60))
	equal(t, deleted, 1)
	equal(t, tr.Len(), int(0))
}

func BenchmarkRangeTree(b *testing.B) {

	mn := make([]*queueInterval, 1000)
	for i := 0; i < 1000; i++ {
		mn[i] = &queueInterval{int64(i) * 10, int64(i)*10 + 10, uint64(i) + 2}
	}

	b.StopTimer()
	b.StartTimer()
	for i := 0; i <= b.N; i++ {
		//tr := NewIntervalTree()
		tr := NewIntervalSkipList()
		//tr := NewIntervalHash()
		for index, q := range mn {
			if index%2 == 0 {
				tr.AddOrMerge(q)
				if index >= 1 {
					tr.IsCompleteOverlap(mn[index/2])
				}
			}
		}
		for index, q := range mn {
			if index%2 == 1 {
				tr.AddOrMerge(q)
				if index >= 1 {
					tr.IsCompleteOverlap(mn[index/2])
				}
			}
		}
		if tr.Len() != int(1) {
			b.Fatal("len not 1 " + tr.ToString())
		}
		l := tr.ToIntervalList()
		tr.DeleteInterval(&queueInterval{
			start:  l[0].Start,
			end:    l[0].End,
			endCnt: l[0].EndCnt,
		})
		for index, q := range mn {
			if index%2 == 1 {
				tr.AddOrMerge(q)
				if index >= 1 {
					tr.DeleteInterval(mn[index/2])
				}
			}
		}

		//if l[0].Start != int64(0) {
		//	b.Fatal("start not 0 " + tr.ToString())
		//}
		//if l[0].End != int64(10000) {
		//	b.Fatal("end not 10000 " + tr.ToString())
		//}
	}
}
