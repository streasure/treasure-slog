@echo off

rem Treasure-Slog 启动示例脚本
rem 用于快速测试不同环境的配置

echo ==============================
echo Treasure-Slog 启动示例脚本
echo ==============================
echo 1. 运行基础示例（开发环境）
echo 2. 运行 HTTP 服务器示例（生产环境）
echo 3. 运行高吞吐量测试（高性能模式）
echo 4. 退出

echo.
set /p choice=请选择要运行的示例 (1-4): 

if "%choice%"=="1" goto basic
if "%choice%"=="2" goto http
if "%choice%"=="3" goto highperf
if "%choice%"=="4" goto exit

echo 无效选择，请重新运行脚本并选择 1-4
pause
goto end

:basic
echo 运行基础示例（开发环境）...
go run examples/basic/main.go configs/config.dev.yaml
goto end

:http
echo 运行 HTTP 服务器示例（生产环境）...
go run examples/http_server/main.go configs/config.prod.yaml
goto end

:highperf
echo 运行高吞吐量测试（高性能模式）...
go run examples/high_throughput/main.go configs/config.highperf.yaml
goto end

:exit
echo 退出脚本
goto end

:end
echo 示例运行完成！
pause
