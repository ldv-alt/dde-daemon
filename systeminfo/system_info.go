package systeminfo

import (
	"dbus/org/freedesktop/udisks2"
	"fmt"
	"io/ioutil"
	"os/exec"
	"pkg.linuxdeepin.com/lib/dbus"
	"pkg.linuxdeepin.com/lib/glib-2.0"
	"pkg.linuxdeepin.com/lib/log"
	dutils "pkg.linuxdeepin.com/lib/utils"
	"strconv"
	"strings"
)

// TODO: as a separate program, nonresident memory

type SystemInfo struct {
	Version    string
	Processor  string
	DiskCap    uint64
	MemoryCap  uint64
	SystemType int64

	logger *log.Logger
}

var (
	errFileNotExist = fmt.Errorf("No such file or directory")
	errValueNull    = fmt.Errorf("Value is null")
)

func getCPUInfoFromFile(config string) (string, error) {
	if !dutils.IsFileExist(config) {
		return "", errFileNotExist
	}

	contents, err := ioutil.ReadFile(config)
	if err != nil {
		return "", err
	}

	var (
		info string
		cnt  int
	)
	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		if strings.Contains(line, "model name") {
			vars := strings.Split(line, ":")
			if len(vars) != 2 {
				break
			}
			cnt++
			if len(info) == 0 {
				info += vars[1]
			}
		}
	}
	if cnt > 1 {
		info = fmt.Sprintf("%s x %v", info, cnt)
	}

	return strings.TrimSpace(info), nil
}

func getVersionFromDeepin(config string) (string, error) {
	if !dutils.IsFileExist(config) {
		return "", errFileNotExist
	}

	kFile := glib.NewKeyFile()
	defer kFile.Free()
	_, err := kFile.LoadFromFile(config,
		glib.KeyFileFlagsKeepTranslations)
	if err != nil {
		return "", err
	}

	version, err := kFile.GetString("Release", "Version")
	if err != nil {
		return "", err
	}
	t, err := kFile.GetLocaleString("Release", "Type", "\x00")
	if err != nil {
		return "", err
	}
	version = version + " " + t
	return version, nil
}

func getVersionFromLsb(lsbfile string) (string, error) {
	if !dutils.IsFileExist(lsbfile) {
		return "", errFileNotExist
	}

	value, err := getValueByKeyFromFile(lsbfile, "DISTRIB_RELEASE", "=")
	if err != nil {
		return "", err
	}

	return value, nil
}

func getMemoryCapFromFile(config string) (uint64, error) {
	if !dutils.IsFileExist(config) {
		return 0, errFileNotExist
	}

	value, err := getValueByKeyFromFile(config, "MemTotal", ":")
	if err != nil {
		return 0, err
	}
	value = strings.TrimSpace(value)
	if len(value) == 0 {
		return 0, errValueNull
	}

	vars := strings.Split(value, " ")
	value = vars[0]
	caps, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}

	return (caps * 1024), nil
}

func getValueByKeyFromFile(filename, key, delim string) (string, error) {
	contents, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}

	var value string
	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		if strings.Contains(line, key) {
			fields := strings.Split(line, delim)
			if len(fields) != 2 {
				break
			}
			value = fields[1]
			break
		}
	}

	return value, nil
}

func getSystemType() (int64, error) {
	cmd := exec.Command("/bin/sh", "-c", "/bin/uname -m")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var sysType int64
	str := strings.ToLower(string(out))
	if strings.Contains(str, "i386") ||
		strings.Contains(str, "i586") ||
		strings.Contains(str, "i686") {
		sysType = 32
	} else if strings.Contains(str, "x86_64") {
		sysType = 64
	}

	return sysType, nil
}

func getDiskCap() (uint64, error) {
	udisk, err := udisks2.NewObjectManager(
		"org.freedesktop.UDisks2",
		"/org/freedesktop/UDisks2")
	if err != nil {
		return 0, err
	}

	var (
		diskCap uint64
		driList []dbus.ObjectPath
	)
	managers, _ := udisk.GetManagedObjects()
	for _, value := range managers {
		if _, ok := value["org.freedesktop.UDisks2.Block"]; ok {
			v := value["org.freedesktop.UDisks2.Block"]["Drive"]
			path := v.Value().(dbus.ObjectPath)
			if path != dbus.ObjectPath("/") {
				flag := false
				l := len(driList)
				for i := 0; i < l; i++ {
					if driList[i] == path {
						flag = true
						break
					}
				}
				if !flag {
					driList = append(driList, path)
				}
			}
		}
	}

	for _, driver := range driList {
		_, driExist := managers[driver]
		rm, _ := managers[driver]["org.freedesktop.UDisks2.Drive"]["Removable"]
		if driExist && !(rm.Value().(bool)) {
			size := managers[driver]["org.freedesktop.UDisks2.Drive"]["Size"]
			diskCap += size.Value().(uint64)
		}
	}

	udisks2.DestroyObjectManager(udisk)
	return diskCap, nil
}

func NewSystemInfo(l *log.Logger) *SystemInfo {
	sys := &SystemInfo{}

	if l == nil {
		l = log.NewLogger("com.deepin.daemon.SystemInfo")
	}
	sys.logger = l

	var err error
	sys.Version, err = getVersionFromDeepin("/etc/deepin-version")
	if err != nil {
		sys.logger.Warning(err)
		sys.Version, err = getVersionFromLsb("/etc/lsb-release")
		if err != nil {
			sys.logger.Error(err)
			return nil
		}
	}

	sys.Processor, err = getCPUInfoFromFile("/proc/cpuinfo")
	if err != nil {
		sys.logger.Error(err)
		return nil
	}

	sys.MemoryCap, err = getMemoryCapFromFile("/proc/meminfo")
	if err != nil {
		sys.logger.Error(err)
		return nil
	}

	sys.SystemType, err = getSystemType()
	if err != nil {
		sys.logger.Error(err)
		return nil
	}

	sys.DiskCap, err = getDiskCap()
	if err != nil {
		sys.logger.Error(err)
		return nil
	}

	return sys
}

var _sysInfo *SystemInfo

func Start() {
	if _sysInfo != nil {
		return
	}

	logger := log.NewLogger("com.deepin.daemon.SystemInfo")
	logger.BeginTracing()

	_sysInfo = NewSystemInfo(logger)
	err := dbus.InstallOnSession(_sysInfo)
	if err != nil {
		logger.Error(err)
		_sysInfo = nil
		logger.EndTracing()
		return
	}
}
func Stop() {
	if _sysInfo == nil {
		return
	}

	_sysInfo.logger.EndTracing()
	dbus.UnInstallObject(_sysInfo)
	_sysInfo = nil
}
