// Package model defines the game, game-file and download-progress data
// structures ported from LGOGDownloader (WTFPL; pinned reference under
// /reference).
//
// Mapping:
//
//	include/gamefile.h        + src/gamefile.cpp  -> gamefile.go
//	include/downloadinfo.h                        -> downloadinfo.go
//
// The gameDetails structures are NOT here: gameDetails itself lives in
// internal/gamedetails (with the filters, the path templates and, since GD1,
// the Galaxy product JSON conversion), so this package holds only the data the
// download and account faces share.
package model
