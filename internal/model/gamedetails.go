package model

// GameDetails mirrors class gameDetails (include/gamedetails.h:20-65).
//
// Only the data members are ported here. The behavioural methods declared in
// the C++ header (filterWithPriorities, makeFilepaths, the per-artifact
// filepath accessors, getDetailsAsJson, getGameFileVector{,Filtered},
// filterWithType, makeCustomFilepath; gamedetails.h:40-57) are implemented in
// src/gamedetails.cpp and are ported with their consumers during the core
// work (list assembly and path/filter features), see package doc.go.
type GameDetails struct {
	Extras        []GameFile
	Installers    []GameFile
	Patches       []GameFile
	Languagepacks []GameFile
	DLCS          []GameDetails

	GameName         string
	GameNameBasegame string
	ProductID        string
	Title            string
	TitleBasegame    string
	Icon             string
	Serials          string
	Changelog        string
	Logo             string
	GameDetailsJSON  string
	ProductJSON      string
}
