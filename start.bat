@echo off

:: 启动脚本 - 演示如何通过命令行参数传递配置文件路径

:: 默认配置
set DEFAULT_CONFIG=configs/config.yaml

:: 显示帮助信息
if "%1"=="--help" ( 
    echo 用法: start.bat [配置文件路径]
    echo 示例:
    echo   start.bat                    :: 使用默认配置文件
    echo   start.bat configs/config.dev.yaml :: 使用开发环境配置
    echo   start.bat configs/config.prod.yaml :: 使用生产环境配置
    echo   start.bat configs/config.highperf.yaml :: 使用高性能配置
    echo   start.bat configs/config.large.yaml :: 使用大日志配置
    exit /b 0
)

:: 检查是否提供了配置文件路径
if "%1"=="" ( 
    echo 使用默认配置文件: %DEFAULT_CONFIG%
    go run cmd/main.go
) else ( 
    echo 使用指定配置文件: %1
    go run cmd/main.go --config=%1
)

pause
