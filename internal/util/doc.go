// Package util hosts the pure helper clusters ported from src/util.cpp of
// LGOGDownloader (WTFPL; pinned reference under /reference).
//
// Clusters and their C++ anchors (util.cpp @ 82b90dbb):
//
//	split.go     tokenize                    (494-511)
//	options.go   getOptionValue/NameString/  (513-579)
//	            parseOptionString
//	strings.go   getStrippedString           (643-662)
//	format.go    makeEtaString x2            (664-701)
//	            makeSizeString/makeRateString (849-903)
//	jsonfile.go  readJsonFile                (905-929)
//	paths.go     getHomeDir/getConfigHome/   (460-492)
//	            getCacheHome
//	replace.go   replaceString/All           (354-378)
//
// Architecture rule: a new helper must belong to one of the clusters above;
// otherwise it must spawn a package with domain semantics (gogxml, galaxy,
// ...) instead of growing util into a general-purpose junk drawer.
package util
