# 批量修改Go模块导入路径的PowerShell脚本
# 将 github.com/charmbracelet/crush 替换为 github.com/xinggaoya/crush

param(
    [string]$OldModule = "github.com/charmbracelet/crush",
    [string]$NewModule = "github.com/xinggaoya/crush"
)

# 颜色输出
function Write-ColorOutput {
    param([string]$Message, [string]$Color = "White")
    Write-Host $Message -ForegroundColor $Color
}

Write-ColorOutput "开始批量修改导入路径..." "Blue"

# 统计信息
$filesModified = 0
$replacements = 0

# 查找所有Go文件
Write-ColorOutput "正在查找需要修改的文件..." "Yellow"
$goFiles = Get-ChildItem -Path . -Filter "*.go" -Recurse | Where-Object { $_.FullName -notlike "*\vendor\*" }
Write-ColorOutput "找到 $($goFiles.Count) 个Go文件" "Blue"

# 修改go.mod文件
if (Test-Path "go.mod") {
    Write-ColorOutput "修改 go.mod..." "Yellow"
    $content = Get-Content "go.mod" -Raw
    if ($content -match [regex]::Escape($OldModule)) {
        $newContent = $content -replace [regex]::Escape($OldModule), $NewModule
        Set-Content "go.mod" -Value $newContent -NoNewline
        Write-ColorOutput "✓ go.mod 已修改" "Green"
        $filesModified++
    } else {
        Write-ColorOutput "✓ go.mod 无需修改" "Green"
    }
}

# 批量修改Go文件中的导入路径
foreach ($file in $goFiles) {
    $content = Get-Content $file.FullName -Raw
    if ($content -match [regex]::Escape($OldModule)) {
        # 创建备份
        Copy-Item $file.FullName "$($file.FullName).bak"
        
        # 替换导入路径
        $newContent = $content -replace [regex]::Escape($OldModule), $NewModule
        Set-Content $file.FullName -Value $newContent -NoNewline
        
        # 统计替换次数
        $replacementsInFile = [regex]::Matches($content, [regex]::Escape($OldModule)).Count
        Write-ColorOutput "✓ $($file.Name) ($replacementsInFile 处替换)" "Green"
        
        # 删除备份
        Remove-Item "$($file.FullName).bak"
        
        $filesModified++
        $replacements += $replacementsInFile
    }
}

# 修改README.md
if (Test-Path "README.md") {
    $content = Get-Content "README.md" -Raw
    if ($content -match [regex]::Escape($OldModule)) {
        $newContent = $content -replace [regex]::Escape($OldModule), $NewModule
        Set-Content "README.md" -Value $newContent -NoNewline
        Write-ColorOutput "✓ README.md 已修改" "Green"
        $filesModified++
    }
}

# 修改其他配置文件
$configFiles = @("*.md", "*.txt", "*.json", "*.yaml", "*.yml")
foreach ($pattern in $configFiles) {
    $files = Get-ChildItem -Path . -Filter $pattern -Exclude "README.md"
    foreach ($file in $files) {
        $content = Get-Content $file.FullName -Raw -ErrorAction SilentlyContinue
        if ($content -and $content -match [regex]::Escape($OldModule)) {
            $newContent = $content -replace [regex]::Escape($OldModule), $NewModule
            Set-Content $file.FullName -Value $newContent -NoNewline
            Write-ColorOutput "✓ $($file.Name) 已修改" "Green"
            $filesModified++
        }
    }
}

# 运行go mod tidy更新依赖
Write-ColorOutput "运行 go mod tidy..." "Yellow"
$tidyResult = go mod tidy 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-ColorOutput "✓ go mod tidy 成功" "Green"
} else {
    Write-ColorOutput "✗ go mod tidy 失败" "Red"
    Write-ColorOutput $tidyResult "Red"
    exit 1
}

# 统计结果
Write-ColorOutput "=== 修改完成 ===" "Blue"
Write-ColorOutput "修改文件数: $filesModified" "Green"
Write-ColorOutput "替换次数: $replacements" "Green"

# 验证修改
Write-ColorOutput "验证修改结果..." "Yellow"
$remaining = 0
Get-ChildItem -Path . -Filter "*.go" -Recurse | Where-Object { $_.FullName -notlike "*\vendor\*" } | ForEach-Object {
    $content = Get-Content $_.FullName -Raw
    if ($content -match [regex]::Escape($OldModule)) {
        $remaining += [regex]::Matches($content, [regex]::Escape($OldModule)).Count
    }
}

if ($remaining -eq 0) {
    Write-ColorOutput "✓ 所有旧模块路径已替换" "Green"
} else {
    Write-ColorOutput "⚠ 仍有 $remaining 处旧模块路径未替换" "Yellow"
    Write-ColorOutput "请手动检查以下文件：" "Yellow"
    Get-ChildItem -Path . -Filter "*.go" -Recurse | Where-Object { $_.FullName -notlike "*\vendor\*" } | ForEach-Object {
        $content = Get-Content $_.FullName -Raw
        if ($content -match [regex]::Escape($OldModule)) {
            Write-ColorOutput "  $($_.FullName)" "Yellow"
        }
    }
}

# 测试编译
Write-ColorOutput "测试编译..." "Yellow"
$buildResult = go build -o "crush-test.exe" . 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-ColorOutput "✓ 编译成功" "Green"
    Remove-Item "crush-test.exe" -ErrorAction SilentlyContinue
} else {
    Write-ColorOutput "✗ 编译失败" "Red"
    Write-ColorOutput $buildResult "Red"
    Write-ColorOutput "请检查修改是否正确" "Red"
    exit 1
}

Write-ColorOutput "🎉 批量修改完成！" "Green"
Write-ColorOutput "现在可以使用以下命令安装：" "Blue"
Write-ColorOutput "go install github.com/xinggaoya/crush@latest" "Yellow"