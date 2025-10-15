#!/usr/bin/env bash

# 批量修改Go模块导入路径的脚本
# 将 github.com/charmbracelet/crush 替换为 github.com/xinggaoya/crush

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}开始批量修改导入路径...${NC}"

# 原模块路径
OLD_MODULE="github.com/charmbracelet/crush"
# 新模块路径  
NEW_MODULE="github.com/xinggaoya/crush"

# 统计信息
FILES_MODIFIED=0
REPLACEMENTS=0

# 查找所有需要修改的文件
echo -e "${YELLOW}正在查找需要修改的文件...${NC}"

# 查找所有Go文件
mapfile -t FILES < <(find . -name "*.go" -type f -not -path "./vendor/*")

echo -e "${BLUE}找到 ${#FILES[@]} 个Go文件${NC}"

# 修改go.mod文件
if [ -f "go.mod" ]; then
    echo -e "${YELLOW}修改 go.mod...${NC}"
    if grep -q "$OLD_MODULE" go.mod; then
        sed -i.bak "s|$OLD_MODULE|$NEW_MODULE|g" go.mod
        rm go.mod.bak
        echo -e "${GREEN}✓ go.mod 已修改${NC}"
        ((FILES_MODIFIED++))
    else
        echo -e "${GREEN}✓ go.mod 无需修改${NC}"
    fi
fi

# 批量修改Go文件中的导入路径
for file in "${FILES[@]}"; do
    if grep -q "$OLD_MODULE" "$file"; then
        # 创建备份
        cp "$file" "$file.bak"
        
        # 替换导入路径
        replacements_in_file=$(sed -i.bak "s|$OLD_MODULE|$NEW_MODULE|g" "$file" && \
                              diff "$file.bak" "$file" | grep "^>" | wc -l || echo 0)
        
        rm "$file.bak"
        
        echo -e "${GREEN}✓ $file ($replacements_in_file 处替换)${NC}"
        ((FILES_MODIFIED++))
        ((REPLACEMENTS += replacements_in_file))
    fi
done

# 修改其他可能包含模块路径的文件
echo -e "${YELLOW}检查其他配置文件...${NC}"

# 检查README.md
if [ -f "README.md" ] && grep -q "$OLD_MODULE" README.md; then
    sed -i.bak "s|$OLD_MODULE|$NEW_MODULE|g" README.md
    rm README.md.bak
    echo -e "${GREEN}✓ README.md 已修改${NC}"
    ((FILES_MODIFIED++))
fi

# 检查其他文档文件
for doc_file in "*.md" "*.txt" "*.json" "*.yaml" "*.yml"; do
    if [ -f "$doc_file" ] && grep -q "$OLD_MODULE" "$doc_file" 2>/dev/null; then
        sed -i.bak "s|$OLD_MODULE|$NEW_MODULE|g" "$doc_file"
        rm "$doc_file.bak"
        echo -e "${GREEN}✓ $doc_file 已修改${NC}"
        ((FILES_MODIFIED++))
    fi
done

# 运行go mod tidy更新依赖
echo -e "${YELLOW}运行 go mod tidy...${NC}"
if go mod tidy; then
    echo -e "${GREEN}✓ go mod tidy 成功${NC}"
else
    echo -e "${RED}✗ go mod tidy 失败${NC}"
    exit 1
fi

# 统计结果
echo -e "${BLUE}=== 修改完成 ===${NC}"
echo -e "${GREEN}修改文件数: $FILES_MODIFIED${NC}"
echo -e "${GREEN}替换次数: $REPLACEMENTS${NC}"

# 验证修改
echo -e "${YELLOW}验证修改结果...${NC}"

# 检查是否还有旧的模块路径
remaining=$(grep -r "$OLD_MODULE" . --include="*.go" --include="*.md" --include="*.json" --exclude-dir=vendor 2>/dev/null | wc -l)

if [ "$remaining" -eq 0 ]; then
    echo -e "${GREEN}✓ 所有旧模块路径已替换${NC}"
else
    echo -e "${YELLOW}⚠ 仍有 $remaining 处旧模块路径未替换${NC}"
    echo -e "${YELLOW}请手动检查以下文件：${NC}"
    grep -r "$OLD_MODULE" . --include="*.go" --include="*.md" --include="*.json" --exclude-dir=vendor 2>/dev/null
fi

# 测试编译
echo -e "${YELLOW}测试编译...${NC}"
if go build -o /tmp/crush-test .; then
    echo -e "${GREEN}✓ 编译成功${NC}"
    rm -f /tmp/crush-test
else
    echo -e "${RED}✗ 编译失败${NC}"
    echo -e "${RED}请检查修改是否正确${NC}"
    exit 1
fi

echo -e "${GREEN}🎉 批量修改完成！${NC}"
echo -e "${BLUE}现在可以使用以下命令安装：${NC}"
echo -e "${YELLOW}go install github.com/xinggaoya/crush@latest${NC}"