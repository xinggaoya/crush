#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import re
import subprocess
import sys

def replace_in_file(filepath, old_pattern, new_pattern):
    """安全地替换文件中的内容，保持编码"""
    try:
        # 尝试UTF-8编码读取
        with open(filepath, 'r', encoding='utf-8') as f:
            content = f.read()
    except UnicodeDecodeError:
        # 如果UTF-8失败，尝试其他编码
        try:
            with open(filepath, 'r', encoding='latin-1') as f:
                content = f.read()
        except Exception as e:
            print(f"无法读取文件 {filepath}: {e}")
            return 0
    
    # 计算替换次数
    old_count = len(re.findall(re.escape(old_pattern), content))
    
    if old_count > 0:
        # 替换内容
        new_content = content.replace(old_pattern, new_pattern)
        
        # 写回文件，保持原编码
        try:
            with open(filepath, 'w', encoding='utf-8') as f:
                f.write(new_content)
        except UnicodeEncodeError:
            # 如果UTF-8写入失败，使用latin-1
            with open(filepath, 'w', encoding='latin-1') as f:
                f.write(new_content)
        
        return old_count
    
    return 0

def main():
    old_module = "github.com/charmbracelet/crush"
    new_module = "github.com/xinggaoya/crush"
    
    print("开始批量修改导入路径...")
    
    files_modified = 0
    total_replacements = 0
    
    # 修改go.mod
    if os.path.exists("go.mod"):
        print("修改 go.mod...")
        replacements = replace_in_file("go.mod", old_module, new_module)
        if replacements > 0:
            print(f"OK go.mod 已修改 ({replacements} 处替换)")
            files_modified += 1
            total_replacements += replacements
        else:
            print("OK go.mod 无需修改")
    
    # 查找并修改所有Go文件
    print("\n查找Go文件...")
    go_files = []
    for root, dirs, files in os.walk('.'):
        # 跳过vendor目录
        if 'vendor' in dirs:
            dirs.remove('vendor')
        
        for file in files:
            if file.endswith('.go'):
                go_files.append(os.path.join(root, file))
    
    print(f"找到 {len(go_files)} 个Go文件")
    
    # 修改Go文件
    for filepath in go_files:
        replacements = replace_in_file(filepath, old_module, new_module)
        if replacements > 0:
            rel_path = os.path.relpath(filepath)
            print(f"OK {rel_path} ({replacements} 处替换)")
            files_modified += 1
            total_replacements += replacements
    
    # 修改README.md
    if os.path.exists("README.md"):
        print("\n修改 README.md...")
        replacements = replace_in_file("README.md", old_module, new_module)
        if replacements > 0:
            print(f"OK README.md 已修改 ({replacements} 处替换)")
            files_modified += 1
            total_replacements += replacements
    
    # 修改其他配置文件
    config_files = ['crush.json', 'schema.json', 'crush.json.example']
    for config_file in config_files:
        if os.path.exists(config_file):
            print(f"\n修改 {config_file}...")
            replacements = replace_in_file(config_file, old_module, new_module)
            if replacements > 0:
                print(f"OK {config_file} 已修改 ({replacements} 处替换)")
                files_modified += 1
                total_replacements += replacements
    
    print("\n运行 go mod tidy...")
    try:
        result = subprocess.run(['go', 'mod', 'tidy'], capture_output=True, text=True)
        if result.returncode == 0:
            print("OK go mod tidy 成功")
        else:
            print("ERROR go mod tidy 失败")
            print(result.stderr)
            return 1
    except FileNotFoundError:
        print("ERROR 找不到 go 命令")
        return 1
    
    print("\n测试编译...")
    try:
        result = subprocess.run(['go', 'build', '.'], capture_output=True, text=True)
        if result.returncode == 0:
            print("OK 编译成功")
            # 清理编译产物
            if os.path.exists("main.exe"):
                os.remove("main.exe")
            if os.path.exists("crush"):
                os.remove("crush")
            if os.path.exists("crush.exe"):
                os.remove("crush.exe")
        else:
            print("ERROR 编译失败")
            print(result.stderr)
            return 1
    except FileNotFoundError:
        print("ERROR 找不到 go 命令")
        return 1
    
    print("\n=== 修改完成 ===")
    print(f"修改文件数: {files_modified}")
    print(f"替换次数: {total_replacements}")
    
    # 验证修改
    print("\n验证修改结果...")
    remaining = 0
    for root, dirs, files in os.walk('.'):
        if 'vendor' in dirs:
            dirs.remove('vendor')
        
        for file in files:
            if file.endswith('.go'):
                filepath = os.path.join(root, file)
                try:
                    with open(filepath, 'r', encoding='utf-8') as f:
                        content = f.read()
                    remaining += len(re.findall(re.escape(old_module), content))
                except:
                    pass
    
    if remaining == 0:
        print("OK 所有旧模块路径已替换")
    else:
        print(f"WARNING 仍有 {remaining} 处旧模块路径未替换")
    
    print("\n批量修改完成！")
    print("现在可以使用以下命令安装：")
    print("go install github.com/xinggaoya/crush@latest")
    
    return 0

if __name__ == "__main__":
    sys.exit(main())