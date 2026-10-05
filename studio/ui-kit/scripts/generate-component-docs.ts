#!/usr/bin/env tsx
/**
 * Автоматическая генерация глоссария компонентов для README.md
 *
 * Сканирует src/ директорию, находит все компоненты и генерирует таблицу
 * с категориями, именами, описаниями и props.
 */

import { readFileSync, writeFileSync, readdirSync, statSync } from 'fs';
import { join, relative, dirname } from 'path';
import { fileURLToPath } from 'url';
import ts from 'typescript';

interface ComponentProp {
  name: string;
  type: string;
  required: boolean;
  description: string;
  kind: 'string' | 'number' | 'boolean' | 'enum' | 'json' | 'node' | 'callback';
  options: string[];
}

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

interface Component {
  name: string;
  category: string;
  description: string;
  file: string;
  hasTests: boolean;
  props: ComponentProp[];
  examples: string[];
  args: Record<string, unknown>;
}

function extractManifest(filePath: string, componentName: string, program: ts.Program, checker: ts.TypeChecker): Pick<Component, 'props' | 'examples' | 'args'> {
  const content = readFileSync(filePath, 'utf-8');
  const source = program.getSourceFile(filePath)
    ?? ts.createSourceFile(filePath, content, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const props: ComponentProp[] = [];
  const examples = [...content.matchAll(/@example[\s\S]*?```(?:tsx|jsx|ts|typescript)?\s*\n([\s\S]*?)```/g)]
    .map((match) => match[1].split('\n').map((line) => line.replace(/^\s*\* ?/, '')).join('\n').trim())
    .filter(Boolean);
  const demoArgsMatch = content.match(/@demoArgs\s+(\{[^\n]*\})/);
  let args: Record<string, unknown> = {};
  if (demoArgsMatch) {
    const parsed: unknown = JSON.parse(demoArgsMatch[1]);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error(`@demoArgs in ${filePath} must be a JSON object`);
    }
    args = parsed as Record<string, unknown>;
  }

  function visit(node: ts.Node): void {
    if (ts.isInterfaceDeclaration(node) && (node.name.text === `${componentName}Props` || componentName === 'UITypography' && /Typography.*Props$/.test(node.name.text))) {
      const symbol = checker.getSymbolAtLocation(node.name);
      if (!symbol) return;
      const resolved = checker.getDeclaredTypeOfSymbol(symbol);
      const declaredNames = new Set(node.members.flatMap((member) => {
        if (!ts.isPropertySignature(member) && !ts.isMethodSignature(member)) return [];
        return member.name && (ts.isIdentifier(member.name) || ts.isStringLiteral(member.name)) ? [member.name.text] : [];
      }));
      // Demo args are an explicit, component-authored subset of inherited APIs
      // (for example, an Ant Design wrapper). Resolve their types from the
      // component's TypeScript props contract without exposing the full base API.
      Object.keys(args).forEach((name) => declaredNames.add(name));
      for (const property of checker.getPropertiesOfType(resolved).filter((candidate) => declaredNames.has(candidate.getName()))) {
        const declaration = property.valueDeclaration ?? property.declarations?.[0];
        if (!declaration) continue;
        const name = property.getName();
        const resolvedType = checker.getTypeOfSymbolAtLocation(property, declaration);
        const type = checker.typeToString(resolvedType);
        const options = resolvedType.isUnion() ? resolvedType.types
          .filter((part) => (part.flags & (ts.TypeFlags.StringLiteral | ts.TypeFlags.NumberLiteral)) !== 0)
          .map((part) => String(part.value)) : [];
        // Keep legacy Ant size aliases in the TypeScript contract, but expose the
        // canonical Studio scale in the interactive playground to avoid duplicates.
        const playgroundOptions = /\bUICompatibleSize\b/.test(type)
          ? options.filter((option) => option === 'sm' || option === 'md' || option === 'lg')
          : options;
        const description = property.getDocumentationComment(checker).map((part) => part.text).join(' ');
        const kind = classifyProp(name, type, playgroundOptions);
        if (!props.some((prop) => prop.name === name)) {
          props.push({ name, type, required: (property.flags & ts.SymbolFlags.Optional) === 0, description, kind, options: playgroundOptions });
        }
      }
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
  const unsupportedArgs = Object.keys(args).filter((name) => !props.some((prop) => prop.name === name));
  if (unsupportedArgs.length > 0) {
    throw new Error(`@demoArgs in ${filePath} contains unsupported props: ${unsupportedArgs.join(', ')}`);
  }
  return { props, examples, args };
}

function classifyProp(name: string, type: string, options: readonly string[]): ComponentProp['kind'] {
  if (name === 'children' || /ReactNode|React\.ReactNode/i.test(type)) return 'node';
  if (/=>|Function|\(.*\)\s*=>/.test(type)) return 'callback';
  if (options.length > 0) return 'enum';
  if (/boolean/.test(type)) return 'boolean';
  if (/number/.test(type)) return 'number';
  if (/string/.test(type) && !/[{\[]/.test(type)) return 'string';
  return 'json';
}

const CATEGORIES = [
  'primitives',
  'core',
  'layout',
  'form',
  'navigation',
  'composite',
  'feedback',
  'overlay',
  'content',
] as const;

const CATEGORY_DESCRIPTIONS: Record<string, string> = {
  primitives: 'Foundation components for layout and typography',
  core: 'Essential UI components',
  layout: 'Layout and container components',
  form: 'Form controls and inputs',
  navigation: 'Navigation and menu components',
  composite: 'Complex composed components',
  feedback: 'Feedback and notification components',
  overlay: 'Modal and overlay components',
  content: 'Content display components',
};

/**
 * Извлекает JSDoc комментарий из файла компонента
 */
function extractDescription(filePath: string): string {
  try {
    const content = readFileSync(filePath, 'utf-8');

    // Ищем первый JSDoc комментарий в начале файла
    const jsdocMatch = content.match(/\/\*\*\s*\n\s*\*\s*(.+?)(?:\s*\n\s*\*\s*\n|\s*\n\s*\*\/)/);
    if (jsdocMatch) {
      return jsdocMatch[1].trim();
    }

    // Ищем export function ComponentName с JSDoc
    const exportMatch = content.match(/\/\*\*\s*\n\s*\*\s*(.+?)\s*\n[\s\S]*?\*\/\s*export\s+function/);
    if (exportMatch) {
      return exportMatch[1].trim();
    }

    // Fallback: берем имя компонента из имени файла
    const fileName = filePath.split('/').pop()?.replace('.tsx', '') || 'Component';
    return fileName;
  } catch (error) {
    return 'Component';
  }
}

/**
 * Сканирует директорию категории и находит все компоненты
 */
function scanCategory(category: string, srcPath: string, program: ts.Program, checker: ts.TypeChecker): Component[] {
  const categoryPath = join(srcPath, category);
  const components: Component[] = [];

  try {
    const files = readdirSync(categoryPath);

    for (const file of files) {
      // Пропускаем index.ts, тесты и не-tsx файлы
      if (file === 'index.ts' || file.endsWith('.test.tsx') || !file.endsWith('.tsx')) {
        continue;
      }

      const componentName = file.replace('.tsx', '');
      const filePath = join(categoryPath, file);
      const testPath = join(categoryPath, `${componentName}.test.tsx`);

      let hasTests = false;
      try {
        statSync(testPath);
        hasTests = true;
      } catch {
        // No test file
      }

      const description = extractDescription(filePath);

      components.push({
        name: componentName,
        category,
        description,
        file: relative(srcPath, filePath),
        hasTests,
        ...extractManifest(filePath, componentName, program, checker),
      });
    }
  } catch (error) {
    // Category directory doesn't exist
  }

  return components;
}

/** Catalog only names that are re-exported from a public category barrel. */
function scanPublicCategory(category: string, srcPath: string, program: ts.Program, checker: ts.TypeChecker): Component[] {
  const categoryPath = join(srcPath, category);
  const barrelPath = join(categoryPath, 'index.ts');
  if (!statExists(barrelPath)) return [];
  const barrel = readFileSync(barrelPath, 'utf-8');
  const exportedFiles = [...barrel.matchAll(/export\s+(?:\*|\{[\s\S]*?\})\s+from\s+['"]\.\/([^'"]+)['"]/g)]
    .map((match) => match[1]);
  return scanCategory(category, srcPath, program, checker).filter((component) => exportedFiles.includes(component.name));
}

function statExists(path: string): boolean {
  try { statSync(path); return true; } catch { return false; }
}

function generateCatalog(components: Component[]): string {
  const catalog = components
    .sort((a, b) => a.category.localeCompare(b.category) || a.name.localeCompare(b.name))
    .map(({ name, category, description, file, props, examples, args }) => ({ name, category, description, source: file, props, examples, args }));
  return `/** Generated by scripts/generate-component-docs.ts. Do not edit by hand. */
export interface UIComponentPropManifest {
  name: string;
  type: string;
  required: boolean;
  description: string;
  kind: 'string' | 'number' | 'boolean' | 'enum' | 'json' | 'node' | 'callback';
  options: string[];
}
export interface UIComponentManifest {
  name: string;
  category: string;
  description: string;
  source: string;
  props: UIComponentPropManifest[];
  examples: string[];
  /** Explicit initial playground args, authored by the component owner. */
  args: Record<string, unknown>;
}
export const uiKitCatalog: UIComponentManifest[] = ${JSON.stringify(catalog, null, 2)};
`;
}

/**
 * Генерирует таблицу компонентов в Markdown
 */
function generateComponentTable(components: Component[]): string {
  const lines: string[] = [];

  // Группируем по категориям
  const byCategory = components.reduce((acc, comp) => {
    if (!acc[comp.category]) {
      acc[comp.category] = [];
    }
    acc[comp.category].push(comp);
    return acc;
  }, {} as Record<string, Component[]>);

  for (const category of CATEGORIES) {
    const categoryComponents = byCategory[category];
    if (!categoryComponents || categoryComponents.length === 0) {
      continue;
    }

    const categoryName = category.charAt(0).toUpperCase() + category.slice(1);
    const categoryDesc = CATEGORY_DESCRIPTIONS[category] || '';

    lines.push(`### ${categoryName}`);
    lines.push('');
    lines.push(categoryDesc);
    lines.push('');
    lines.push('| Component | Description | Tests |');
    lines.push('|-----------|-------------|-------|');

    // Сортируем компоненты по имени
    categoryComponents.sort((a, b) => a.name.localeCompare(b.name));

    for (const comp of categoryComponents) {
      const testBadge = comp.hasTests ? '✅' : '❌';
      lines.push(`| **${comp.name}** | ${comp.description} | ${testBadge} |`);
    }

    lines.push('');
  }

  return lines.join('\n');
}

/**
 * Генерирует статистику компонентов
 */
function generateStats(components: Component[]): string {
  const total = components.length;
  const withTests = components.filter(c => c.hasTests).length;
  const testCoverage = total > 0 ? Math.round((withTests / total) * 100) : 0;

  const byCategory = components.reduce((acc, comp) => {
    acc[comp.category] = (acc[comp.category] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  const lines: string[] = [];
  lines.push('## 📊 Component Stats');
  lines.push('');
  lines.push(`- **Total Components:** ${total}`);
  lines.push(`- **With Tests:** ${withTests}/${total} (${testCoverage}%)`);
  lines.push('');
  lines.push('**By Category:**');
  lines.push('');

  for (const category of CATEGORIES) {
    const count = byCategory[category] || 0;
    if (count > 0) {
      const categoryName = category.charAt(0).toUpperCase() + category.slice(1);
      lines.push(`- ${categoryName}: ${count}`);
    }
  }

  lines.push('');

  return lines.join('\n');
}

/**
 * Обновляет README.md с автоматически сгенерированным контентом
 */
function updateReadme(readmePath: string, componentTable: string, stats: string): void {
  const content = readFileSync(readmePath, 'utf-8');

  // Маркеры для автоматической генерации
  const START_MARKER = '<!-- AUTO-GENERATED:START -->';
  const END_MARKER = '<!-- AUTO-GENERATED:END -->';

  const startIndex = content.indexOf(START_MARKER);
  const endIndex = content.indexOf(END_MARKER);

  if (startIndex === -1 || endIndex === -1) {
    console.error('⚠️  Markers not found in README.md. Add these markers:');
    console.error('   <!-- AUTO-GENERATED:START -->');
    console.error('   <!-- AUTO-GENERATED:END -->');
    process.exit(1);
  }

  const before = content.substring(0, startIndex + START_MARKER.length);
  const after = content.substring(endIndex);

  const newContent = [
    before,
    '',
    stats,
    '## 📚 Component Reference',
    '',
    componentTable,
    after,
  ].join('\n');

  writeFileSync(readmePath, newContent, 'utf-8');
}

/**
 * Main
 */
function main() {
  const catalogOnly = process.argv.includes('--catalog-only');
  console.log('🔍 Scanning components...');

  const rootDir = join(__dirname, '..');
  const srcPath = join(rootDir, 'src');
  const readmePath = join(rootDir, 'README.md');
  const catalogPath = join(srcPath, 'catalog.generated.ts');
  const componentFiles = CATEGORIES.flatMap((category) => readdirSync(join(srcPath, category))
    .filter((file) => file.endsWith('.tsx') && !file.endsWith('.test.tsx'))
    .map((file) => join(srcPath, category, file)));
  const program = ts.createProgram(componentFiles, {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler,
    jsx: ts.JsxEmit.ReactJSX,
    strict: true,
    skipLibCheck: true,
    esModuleInterop: true,
  });
  const checker = program.getTypeChecker();

  // Сканируем все категории
  const allComponents: Component[] = [];
  for (const category of CATEGORIES) {
    const components = scanCategory(category, srcPath, program, checker);
    allComponents.push(...components);
    if (components.length > 0) {
      console.log(`  ✓ ${category}: ${components.length} components`);
    }
  }

  console.log(`\n📊 Total: ${allComponents.length} components\n`);

  // Keep the README inventory and machine-readable catalog in sync on the kit build.
  if (!catalogOnly) {
    const componentTable = generateComponentTable(allComponents);
    const stats = generateStats(allComponents);
    updateReadme(readmePath, componentTable, stats);
  }
  const publicComponents = CATEGORIES.flatMap((category) => scanPublicCategory(category, srcPath, program, checker));
  writeFileSync(catalogPath, generateCatalog(publicComponents), 'utf-8');

  console.log(`✅ ${catalogOnly ? 'Public catalog' : 'README.md and public catalog'} (${publicComponents.length} public components) updated!\n`);

  // Показываем статистику тестов
  const withTests = allComponents.filter(c => c.hasTests).length;
  const testCoverage = Math.round((withTests / allComponents.length) * 100);
  console.log(`📈 Test Coverage: ${withTests}/${allComponents.length} (${testCoverage}%)`);
}

main();
