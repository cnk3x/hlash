linuxBuild::
	wsl -e go build -trimpath -v -ldflags '-s -w' -o bin/hlash ./
	wsl -e upx bin/hlash

windowsBuild::
	go build -trimpath -v -ldflags '-s -w' -o bin/hlash.exe ./
	upx bin/hlash.exe
	copy .\bin\hlash.exe D:\services\hlash\hlash.exe
