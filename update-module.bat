@echo off
setlocal enabledelayedexpansion

echo 开始批量修改导入路径...

set OLD_MODULE=github.com/charmbracelet/crush
set NEW_MODULE=github.com/xinggaoya/crush

set /a files_modified=0
set /a replacements=0

echo 修改 go.mod...
findstr /C:"%OLD_MODULE%" go.mod >nul 2>&1
if %errorlevel% equ 0 (
    powershell -Command "(Get-Content 'go.mod' -Raw) -replace '%OLD_MODULE%', '%NEW_MODULE%' | Set-Content 'go.mod.new' -NoNewline; Move-Item 'go.mod.new' 'go.mod' -Force"
    echo ✓ go.mod 已修改
    set /a files_modified+=1
) else (
    echo ✓ go.mod 无需修改
)

echo.
echo 查找并修改Go文件...
for /r . %%f in (*.go) do (
    if not "%%~pf"=="%cd%\vendor\" (
        findstr /C:"%OLD_MODULE%" "%%f" >nul 2>&1
        if !errorlevel! equ 0 (
            echo 修改 %%~nxf...
            powershell -Command "(Get-Content '%%f' -Raw) -replace '%OLD_MODULE%', '%NEW_MODULE%' | Set-Content '%%f.new' -NoNewline; Move-Item '%%f.new' '%%f' -Force"
            echo ✓ %%~nxf 已修改
            set /a files_modified+=1
        )
    )
)

echo.
echo 修改README.md...
if exist README.md (
    findstr /C:"%OLD_MODULE%" README.md >nul 2>&1
    if %errorlevel% equ 0 (
        powershell -Command "(Get-Content 'README.md' -Raw) -replace '%OLD_MODULE%', '%NEW_MODULE%' | Set-Content 'README.md.new' -NoNewline; Move-Item 'README.md.new' 'README.md' -Force"
        echo ✓ README.md 已修改
        set /a files_modified+=1
    )
)

echo.
echo 运行 go mod tidy...
go mod tidy
if %errorlevel% equ 0 (
    echo ✓ go mod tidy 成功
) else (
    echo ✗ go mod tidy 失败
    pause
    exit /b 1
)

echo.
echo 测试编译...
go build -o crush-test.exe .
if %errorlevel% equ 0 (
    echo ✓ 编译成功
    del crush-test.exe
) else (
    echo ✗ 编译失败
    pause
    exit /b 1
)

echo.
echo === 修改完成 ===
echo 修改文件数: %files_modified%
echo.
echo 🎉 批量修改完成！
echo 现在可以使用以下命令安装：
echo go install github.com/xinggaoya/crush@latest

pause