package store

import (
	"errors"
	"fmt"
	"io"
	"log"
	"time"
)

// store/IOContext.java

const (
	IO_CONTEXT_TYPE_MERGE   = 1
	IO_CONTEXT_TYPE_READ    = 2
	IO_CONTEXT_TYPE_FLUSH   = 3
	IO_CONTEXT_TYPE_DEFAULT = 4
)

type IOContextType int

var (
	IO_CONTEXT_DEFAULT  = NewIOContextFromType(IOContextType(IO_CONTEXT_TYPE_DEFAULT))
	IO_CONTEXT_READONCE = NewIOContextBool(true)
	IO_CONTEXT_READ     = NewIOContextBool(false)
)

/*
IOContext holds additional details on the merge/search context. A
IOContext object can never be initialized as nil as passed as a
parameter to either OpenInput() or CreateOutput()
*/
type IOContext struct {
	context   IOContextType
	MergeInfo *MergeInfo
	FlushInfo *FlushInfo
	readOnce  bool
}

func NewIOContextForFlush(flushInfo *FlushInfo) IOContext {
	assert(flushInfo != nil)
	return IOContext{
		context:   IOContextType(IO_CONTEXT_TYPE_FLUSH),
		readOnce:  false,
		FlushInfo: flushInfo,
	}
}

func NewIOContextFromType(context IOContextType) IOContext {
	assert2(context != IO_CONTEXT_TYPE_MERGE, "Use NewIOContextForMerge() to create a MERGE IOContext")
	assert2(context != IO_CONTEXT_TYPE_FLUSH, "Use NewIOContextForFlush() to create a FLUSH IOContext")
	return IOContext{
		context:  context,
		readOnce: false,
	}
}

func NewIOContextBool(readOnce bool) IOContext {
	return IOContext{
		context:  IOContextType(IO_CONTEXT_TYPE_READ),
		readOnce: readOnce,
	}
}

func NewIOContextForMerge(mergeInfo *MergeInfo) IOContext {
	assert2(mergeInfo != nil, "MergeInfo must not be nil if context is MERGE")
	return IOContext{
		context:   IOContextType(IO_CONTEXT_TYPE_MERGE),
		MergeInfo: mergeInfo,
		readOnce:  false,
	}
}

func (ctx IOContext) String() string {
	return fmt.Sprintf("IOContext [context=%v, mergeInfo=%v, flushInfo=%v, readOnce=%v",
		ctx.context, ctx.MergeInfo, ctx.FlushInfo, ctx.readOnce)
}

type FlushInfo struct {
	NumDocs              int
	EstimatedSegmentSize int64
}

type MergeInfo struct {
	TotalDocCount       int
	EstimatedMergeBytes int64
	IsExternal          bool
	MergeMaxNumSegments int
}

// store/Lock.java

// How long obtain() waits, in milliseconds,
// in between attempts to acquire the lock.
const LOCK_POOL_INTERVAL = 1000

// Pass this value to obtain() to try
// forever to obtain the lock
const LOCK_OBTAIN_WAIT_FOREVER = -1

/*
An interprocess mutex lock.

Typical use might look like:

	WithLock(directory.MakeLock("my.lock"), func() interface{} {
		// code to execute while locked
	})
*/
type Lock interface {
	// Attempts to obtain exclusive access and immediately return
	// upon success or failure
	Obtain() (ok bool, err error)
	// Attempts to obtain an exclusive lock within amount of time
	// given. Pools once per LOCK_POLL_INTERVAL (currently 1000)
	// milliseconds until lockWaitTimeout is passed.
	ObtainWithin(lockWaitTimeout int64) (ok bool, err error)
	// Releases exclusive access.
	Release() error
	// Returns true if the resource is currently locked. Note that one
	// must still call obtain() before using the resource.
	IsLocked() bool
}

type LockImpl struct {
	self Lock
	// If a lock obtain called, this failureReason may be set with the
	// "root cause" error as to why the lock was not obtained
	failureReason error
}

func NewLockImpl(self Lock) *LockImpl {
	return &LockImpl{self: self}
}

func (lock *LockImpl) ObtainWithin(lockWaitTimeout int64) (locked bool, err error) {
	lock.failureReason = nil
	locked, err = lock.self.Obtain()
	if err != nil {
		return
	}
	assert2(lockWaitTimeout >= 0 || lockWaitTimeout == LOCK_OBTAIN_WAIT_FOREVER, fmt.Sprintf(
		"lockWaitTimeout should be LOCK_OBTAIN_WAIT_FOREVER or a non-negative number (got %v)", lockWaitTimeout))

	maxSleepCount := lockWaitTimeout / LOCK_POOL_INTERVAL
	for sleepCount := int64(0); !locked; locked, err = lock.self.Obtain() {
		if lockWaitTimeout != LOCK_OBTAIN_WAIT_FOREVER && sleepCount >= maxSleepCount {
			reason := fmt.Sprintf("Lock obtain time out: %v", lock)
			if lock.failureReason != nil {
				reason = fmt.Sprintf("%v: %v", reason, lock.failureReason)
			}
			err = errors.New(reason)
			return
		}
		sleepCount++
		time.Sleep(LOCK_POOL_INTERVAL * time.Millisecond)
	}
	return
}

// Utility to execute code with exclusive access.
func WithLock(lock Lock, lockWaitTimeout int64, body func() interface{}) interface{} {
	panic("not implemeted yet")
}

type LockFactory interface {
	Make(name string) Lock
	Clear(name string) error
	SetLockPrefix(prefix string)
	LockPrefix() string
}

type LockFactoryImpl struct {
	lockPrefix string
}

func (f *LockFactoryImpl) SetLockPrefix(prefix string) {
	f.lockPrefix = prefix
}

func (f *LockFactoryImpl) LockPrefix() string {
	return f.lockPrefix
}

type FSLockFactory struct {
	*LockFactoryImpl
	lockDir string // can not be set twice
}

func newFSLockFactory() *FSLockFactory {
	ans := &FSLockFactory{}
	ans.LockFactoryImpl = &LockFactoryImpl{}
	return ans
}

func (f *FSLockFactory) setLockDir(lockDir string) {
	if f.lockDir != "" {
		panic("You can set the lock directory for this factory only once.")
	}
	f.lockDir = lockDir
}

func (f *FSLockFactory) getLockDir() string {
	return f.lockDir
}

func (f *FSLockFactory) Clear(name string) error {
	panic("invalid")
}

func (f *FSLockFactory) Make(name string) Lock {
	panic("invalid")
}

func (f *FSLockFactory) String() string {
	return fmt.Sprintf("FSLockFactory@%v", f.lockDir)
}

type Directory interface {
	io.Closer
	// Files related methods
	ListAll() (paths []string, err error)
	// Returns true iff a file with the given name exists.
	FileExists(name string) bool
	// Removes an existing file in the directory.
	DeleteFile(name string) error
	// Returns the length of a file in the directory. This method
	// follows the following contract:
	// 	- Must return error if the file doesn't exists.
	// 	- Returns a value >=0 if the file exists, which specifies its
	// length.
	FileLength(name string) (n int64, err error)
	// Creates a new, empty file in the directory with the given name.
	// Returns a stream writing this file.
	CreateOutput(name string, ctx IOContext) (out IndexOutput, err error)
	// Ensure that any writes to these files ar emoved to stable
	// storage. Lucene uses this to properly commit changes to the
	// index, to prevent a machine/OS crash from corrupting the index.
	//
	// NOTE: Clients may call this method for same files over and over
	// again, so some impls might optimize for that. For other impls
	// the operation can be a noop, for various reasons.
	Sync(names []string) error
	OpenInput(name string, context IOContext) (in IndexInput, err error)
	// Locks related methods
	MakeLock(name string) Lock
	ClearLock(name string) error
	SetLockFactory(lockFactory LockFactory)
	LockFactory() LockFactory
	LockID() string
	// Utilities
	// Copy(to Directory, src, dest string, ctx IOContext) error
	// Experimental methods
	CreateSlicer(name string, ctx IOContext) (slicer IndexInputSlicer, err error)

	EnsureOpen()
}

type directoryService interface {
	OpenInput(name string, context IOContext) (in IndexInput, err error)
}

type DirectoryImpl struct {
	directoryService
	IsOpen      bool
	lockFactory LockFactory
}

func NewDirectoryImpl(self directoryService) *DirectoryImpl {
	return &DirectoryImpl{directoryService: self, IsOpen: true}
}

func (d *DirectoryImpl) MakeLock(name string) Lock {
	return d.lockFactory.Make(name)
}

func (d *DirectoryImpl) ClearLock(name string) error {
	if d.lockFactory != nil {
		return d.lockFactory.Clear(name)
	}
	return nil
}

func (d *DirectoryImpl) SetLockFactory(lockFactory LockFactory) {
	assert(d != nil && lockFactory != nil)
	d.LockID()
	d.lockFactory = lockFactory
	d.lockFactory.SetLockPrefix(d.LockID())
}

func assert(ok bool) {
	if !ok {
		panic("assert fail")
	}
}

func (d *DirectoryImpl) LockFactory() LockFactory {
	return d.lockFactory
}

/*
Return a string identifier that uniquely differentiates
this Directory instance from other Directory instances.
This ID should be the same if two Directory instances
(even in different JVMs and/or on different machines)
are considered "the same index".  This is how locking
"scopes" to the right index.
*/
func (d *DirectoryImpl) LockID() string {
	return fmt.Sprintf("%v", d)
}

func (d *DirectoryImpl) String() string {
	return fmt.Sprintf("Directory lockFactory=%v", d.lockFactory)
}

func (d *DirectoryImpl) CreateSlicer(name string, context IOContext) (is IndexInputSlicer, err error) {
	panic("Should be overrided, I guess")
	d.EnsureOpen()
	base, err := d.OpenInput(name, context)
	if err != nil {
		return nil, err
	}
	return simpleIndexInputSlicer{base}, nil
}

func (d *DirectoryImpl) EnsureOpen() {
	if !d.IsOpen {
		log.Print("This Directory is closed.")
		panic("this Directory is closed")
	}
}

type IndexInputSlicer interface {
	io.Closer
	openSlice(desc string, offset, length int64) IndexInput
	openFullSlice() IndexInput
}

type simpleIndexInputSlicer struct {
	base IndexInput
}

func (is simpleIndexInputSlicer) openSlice(desc string, offset, length int64) IndexInput {
	return newSlicedIndexInput(fmt.Sprintf("SlicedIndexInput(%v in %v)", desc, is.base),
		is.base, offset, length)
}

func (is simpleIndexInputSlicer) Close() error {
	return is.base.Close()
}

func (is simpleIndexInputSlicer) openFullSlice() IndexInput {
	return is.base
}

type SlicedIndexInput struct {
	*BufferedIndexInput
	base       IndexInput
	fileOffset int64
	length     int64
}

func newSlicedIndexInput(desc string, base IndexInput, fileOffset, length int64) *SlicedIndexInput {
	return newSlicedIndexInputBySize(desc, base, fileOffset, length, BUFFER_SIZE)
}

func newSlicedIndexInputBySize(desc string, base IndexInput, fileOffset, length int64, bufferSize int) *SlicedIndexInput {
	ans := &SlicedIndexInput{base: base, fileOffset: fileOffset, length: length}
	super := newBufferedIndexInputBySize(fmt.Sprintf(
		"SlicedIndexInput(%v in %v slice=%v:%v)", desc, base, fileOffset, fileOffset+length), bufferSize)
	super.SeekReader = ans
	super.LengthCloser = ans
	ans.BufferedIndexInput = super
	return ans
}

func (in *SlicedIndexInput) readInternal(buf []byte) (err error) {
	start := in.FilePointer()
	if start+int64(len(buf)) > in.length {
		return errors.New(fmt.Sprintf("read past EOF: %v", in))
	}
	in.base.Seek(in.fileOffset + start)
	return in.base.ReadBytesBuffered(buf, false)
}

func (in *SlicedIndexInput) seekInternal(pos int64) error {
	return nil // nothing
}

func (in *SlicedIndexInput) Close() error {
	return in.base.Close()
}

func (in *SlicedIndexInput) Length() int64 {
	return in.length
}

func (in *SlicedIndexInput) Clone() (ans IndexInput) {
	log.Printf("DEGBU before clone: %v", in)
	defer func() {
		log.Printf("DEBUG after clone: %v", ans)
	}()
	return &SlicedIndexInput{
		in.BufferedIndexInput.Clone().(*BufferedIndexInput),
		in.base.Clone(),
		in.fileOffset,
		in.length,
	}
}
