@echo off
rem Encoding: GBK (CP936). Keep this file in GBK; saving as UTF-8 breaks parsing on Chinese Windows.
cd /d "%~dp0"

rem Treasure-Slog 启动示例脚本
rem 用于快速测试不同环境的配置

echo ==============================
echo Treasure-Slog 启动示例脚本
echo ==============================
echo 1. 运行基础示例（开发环境）
echo 2. 运行 HTTP 服务器示例（生产环境）
echo 3. 运行高吞吐量测试（高性能模式）
echo 4. 运行全局 logger 示例（默认配置）
echo 5. 退出

echo.
set /p choice=请选择要运行的示例 (1-5):

if "%choice%"=="1" goto basic
if "%choice%"=="2" goto http
if "%choice%"=="3" goto highperf
if "%choice%"=="4" goto global
if "%choice%"=="5" goto exit

echo 无效选择，请重新运行脚本并选择 1-5
pause
goto end

:basic
echo 运行基础示例（开发环境）...
go run examples/basic/main.go "configs/tlog.dev.yaml"
goto end

:http
echo 运行 HTTP 服务器示例（生产环境）...
go run examples/http_server/main.go "configs/tlog.prod.yaml"
goto end

:highperf
echo 运行高吞吐量测试（高性能模式）...
go run examples/high_throughput/main.go "configs/tlog.highperf.yaml"
goto end

:global
echo 运行全局 logger 示例（默认配置）...
go run examples/global/main.go "configs/tlog.yaml"
goto end

:exit
echo 退出脚本
goto end

:end
echo 示例运行完成！pause
