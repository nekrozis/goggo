// Package model defines the game, game-file and download-progress data
// structures ported from LGOGDownloader (WTFPL; pinned reference under
// /reference).
//
// Mapping:
//
//	include/gamefile.h        + src/gamefile.cpp  -> gamefile.go
//	include/gamedetails.h                         -> gamedetails.go
//	include/downloadinfo.h                        -> downloadinfo.go
//
// Behavioural methods declared on gameDetails in the C++ header (filters,
// path templates, JSON rendering, see gamedetails.h:40-57) are NOT ported
// here. They live in src/gamedetails.cpp and land together with the features
// that consume them (list assembly in internal/core, S11; directory template
// and filtering work later in the core split).
package model
