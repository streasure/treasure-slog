@echo off
rem Encoding: GBK (CP936). Keep this file in GBK; saving as UTF-8 breaks parsing on Chinese Windows.
cd /d "%~dp0"

:: 启动脚本 - 演示如何通过位置参数传递配置文件路径

:: 默认配置
set DEFAULT_CONFIG=configs/tlog.yaml

:: 显示帮助信息
if "%~1"=="--help" goto help
if "%~1"=="-h" goto help

:: 检查是否提供了配置文件路径
if "%~1"=="" (
    echo 使用默认配置文件: %DEFAULT_CONFIG%
    go run cmd/main.go "%DEFAULT_CONFIG%"
) else (
    echo 使用指定配置文件: %~1
    go run cmd/main.go "%~1"
)

pause
exit /b 0

:help
echo 用法: start.bat [配置文件路径]
echo 示例:
echo   start.bat                    :: 使用默认配置文件
echo   start.bat configs/tlog.dev.yaml :: 使用开发环境配置
echo   start.bat configs/tlog.prod.yaml :: 使用生产环境配置
echo   start.bat configs/tlog.highperf.yaml :: 使用高性能配置
exit /b 0
