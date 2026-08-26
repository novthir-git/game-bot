package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// 覆盖层不能靠「把 local.yaml 再解码一遍到同一个结构体上」来实现。
//
// 对结构体字段，yaml.v3 是逐字段合并的，所以 adb.port 这类覆盖看起来是对的；
// 但对 map 字段（这里是 tasks 下的 map[string]Task），它解码每个 value 时
// 会新建一个零值再整体替换那条 entry，完全不看原有的值。
// 结果是 local.yaml 里只要提到某个任务名，该任务在 tasks.yaml 里的
// enabled/interval_sec/reset_daily 等字段会被一并清零——
// 而 enabled 被清成 false 的任务会被静默跳过，全程没有任何报错。
//
// 所以合并必须发生在解码之前，在 YAML 节点树上做。

// mergeNode 把 over 深度合并进 base。
//
// 映射节点：同名 key 递归合并，base 中不存在的 key 追加。
// 标量与序列：整体替换（序列不做逐项合并——「把列表换成另一个列表」
// 是覆盖列表时唯一符合直觉的语义）。
//
// 返回 over 中那些在 base 里原本不存在的键路径，供调用方识别配置笔误。
func mergeNode(base, over *yaml.Node, prefix string) []string {
	base, over = unwrapDoc(base), unwrapDoc(over)
	if base == nil || over == nil {
		return nil
	}
	if base.Kind != yaml.MappingNode || over.Kind != yaml.MappingNode {
		*base = *over
		return nil
	}

	var added []string
	for i := 0; i+1 < len(over.Content); i += 2 {
		key, val := over.Content[i], over.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}

		if bi := findKey(base, key.Value); bi >= 0 {
			bval := base.Content[bi+1]
			if bval.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode {
				added = append(added, mergeNode(bval, val, path)...)
			} else {
				base.Content[bi+1] = val
			}
			continue
		}
		base.Content = append(base.Content, key, val)
		// 整棵子树都是新增的，必须把它下面每一个叶子路径都记下来，
		// 而不能只记顶层键名。调用方要对三个文件的结果求交集来判断
		// 「这个键在任何配置里都不存在」，两边的路径粒度必须一致：
		// 一边记 adb.prot、另一边只记 adb，交集就永远是空的。
		added = append(added, leafPaths(val, path)...)
	}
	return added
}

// leafPaths 列出一棵节点树下所有叶子的键路径。
func leafPaths(n *yaml.Node, prefix string) []string {
	n = unwrapDoc(n)
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return []string{prefix}
	}
	var out []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, leafPaths(n.Content[i+1], prefix+"."+n.Content[i].Value)...)
	}
	return out
}

func unwrapDoc(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	return n
}

func findKey(m *yaml.Node, name string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == name {
			return i
		}
	}
	return -1
}

// applyOverlay 把 overlay 合并进 base 的节点树，再解码到 dst。
//
// 解码用宽松模式：同一份 overlay 会被叠到三个不同的配置结构体上，
// 属于其他文件的键在这里必然是「未知字段」。
// base 文件自身的拼写错误由调用方先做的一次严格解码负责拦截。
func applyOverlay(baseRaw, overlayRaw []byte, dst any) (added []string, err error) {
	var baseNode, overNode yaml.Node
	if err := yaml.Unmarshal(baseRaw, &baseNode); err != nil {
		return nil, fmt.Errorf("解析基础配置: %w", err)
	}
	if err := yaml.Unmarshal(overlayRaw, &overNode); err != nil {
		return nil, fmt.Errorf("解析覆盖层: %w", err)
	}
	if unwrapDoc(&overNode) == nil {
		return nil, nil // 空的 local.yaml，无事可做
	}
	added = mergeNode(&baseNode, &overNode, "")
	if err := baseNode.Decode(dst); err != nil {
		return nil, fmt.Errorf("解码合并后的配置: %w", err)
	}
	return added, nil
}

// intersect 返回同时出现在所有分组里的元素。
//
// 用途：一个键路径只有在三个配置文件里都不存在时，才算真的未知。
// 它出现在 device.yaml 的「未知列表」里很正常——它可能属于 tasks.yaml。
func intersect(groups [][]string) []string {
	if len(groups) == 0 {
		return nil
	}
	count := map[string]int{}
	for _, g := range groups {
		seen := map[string]bool{}
		for _, k := range g {
			if !seen[k] {
				seen[k] = true
				count[k]++
			}
		}
	}
	var out []string
	for k, c := range count {
		if c == len(groups) {
			out = append(out, k)
		}
	}
	return out
}
