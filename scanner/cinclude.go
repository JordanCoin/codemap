package scanner

import (
	pathpkg "path"
	"path/filepath"
	"strings"
)

// resolveCInclude resolves a C or C++ #include the way a compiler searches,
// short of knowing the -I flags. An angle-bracket spelling arrives as "<x>";
// see angleOnlyIncludes.
//
// ponytail: the -I order is unknown, so a project-wide match stands in for it
// and equally close matches are all kept (src/win/internal.h and
// src/unix/internal.h from src/fs.c). Reading compile_commands.json would
// settle those.
func resolveCInclude(imp, fromDir string, idx *fileIndex, language string) []string {
	imp = strings.Trim(imp, "\"'`")
	angle := strings.HasPrefix(imp, "<")
	imp = strings.Trim(imp, "<>")
	if strings.HasPrefix(imp, ".") {
		if files := tryExactMatch(filepath.Join(fromDir, imp), idx, language); len(files) > 0 {
			return files
		}
		return includeClimbingPastRoot(filepath.Join(fromDir, imp), idx, language)
	}
	// An include names a file literally, so <memory> never means memory.h.
	if pathpkg.Ext(imp) == "" {
		return nil
	}
	if !angle {
		if files := tryExactMatch(filepath.Join(fromDir, imp), idx, language); len(files) > 0 {
			return files
		}
	}
	if angle && standardCHeaders[imp] {
		return nil
	}
	return closestFiles(compatibleFiles(language, idx.suffixMatches(imp)), tryExactMatch(imp, idx, language), filepath.ToSlash(fromDir))
}

// includeClimbingPastRoot handles an include that climbs above the includer's
// directories. A compiler also tries it from each -I directory, so with
// -Isrc/sub "../../include/x.h" is the repo's include/x.h. A bare "../x.h" has no
// directory left to corroborate the match, so it stays unresolved.
func includeClimbingPastRoot(path string, idx *fileIndex, language string) []string {
	path = keyPath(path)
	if !isAboveRoot(path) {
		return nil
	}
	rest := path
	for isAboveRoot(rest) {
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, ".."), "/")
	}
	if !strings.Contains(rest, "/") || idx.exact(rest) != 1 || !languagesCompatible(language, DetectLanguage(rest)) {
		return nil
	}
	return []string{rest}
}

// closestFiles keeps the matches sharing the most leading directories with
// the includer. A repo-root file wins a tie, because -I. is the usual flag.
func closestFiles(matches, atRoot []string, fromDir string) []string {
	best, closest := 0, []string(nil)
	for _, match := range matches {
		shared := sharedDirectories(pathpkg.Dir(match), fromDir)
		if shared > best {
			best, closest = shared, nil
		}
		if shared == best {
			closest = append(closest, match)
		}
	}
	if best == 0 && len(atRoot) > 0 {
		return atRoot
	}
	return closest
}

func sharedDirectories(left, right string) int {
	leftParts, rightParts := strings.Split(left, "/"), strings.Split(right, "/")
	shared := 0
	for shared < len(leftParts) && shared < len(rightParts) && leftParts[shared] == rightParts[shared] {
		shared++
	}
	return shared
}

// angleOnlyIncludes returns the includes a file spells only with angle
// brackets. A file spelling one both ways, usually across #if branches, can
// mean the project's copy, so it keeps the quoted search.
func angleOnlyIncludes(refs []ImportReference) map[string]bool {
	var angle map[string]bool
	for _, ref := range refs {
		if ref.Kind == cAngleInclude {
			if angle == nil {
				angle = make(map[string]bool)
			}
			angle[ref.Path] = true
		}
	}
	for _, ref := range refs {
		if ref.Kind == cQuotedInclude {
			delete(angle, ref.Path)
		}
	}
	return angle
}

const (
	cAngleInclude  = "c-angle-include"
	cQuotedInclude = "c-quoted-include"
)

// cIncludeKind reads the include's spelling from its source text.
func cIncludeKind(text string) string {
	if open := strings.IndexAny(text, "<\""); open >= 0 && text[open] == '<' {
		return cAngleInclude
	}
	return cQuotedInclude
}

// standardCHeaders are the C, POSIX and Windows SDK headers a platform
// provides. A project file sharing one of these names (include/uv/errno.h) is
// not what <errno.h> means.
var standardCHeaders = func() map[string]bool {
	headers := make(map[string]bool)
	for _, name := range strings.Fields(`
		assert.h complex.h ctype.h errno.h fenv.h float.h inttypes.h iso646.h limits.h locale.h
		math.h setjmp.h signal.h stdalign.h stdarg.h stdatomic.h stdbit.h stdbool.h stdckdint.h
		stddef.h stdint.h stdio.h stdlib.h stdnoreturn.h string.h tgmath.h threads.h time.h
		uchar.h wchar.h wctype.h
		aio.h arpa/inet.h cpio.h dirent.h dlfcn.h fcntl.h fmtmsg.h fnmatch.h ftw.h glob.h grp.h
		iconv.h langinfo.h libgen.h monetary.h mqueue.h ndbm.h net/if.h netdb.h netinet/in.h
		netinet/tcp.h nl_types.h poll.h pthread.h pwd.h regex.h sched.h search.h semaphore.h
		spawn.h strings.h stropts.h syslog.h tar.h termios.h trace.h ulimit.h unistd.h utime.h
		utmpx.h wordexp.h malloc.h memory.h alloca.h
		sys/ipc.h sys/mman.h sys/msg.h sys/resource.h sys/select.h sys/sem.h sys/shm.h
		sys/socket.h sys/stat.h sys/statvfs.h sys/time.h sys/times.h sys/types.h sys/uio.h
		sys/un.h sys/utsname.h sys/wait.h
		windows.h winsock.h winsock2.h ws2tcpip.h io.h direct.h process.h conio.h
	`) {
		headers[name] = true
	}
	return headers
}()
