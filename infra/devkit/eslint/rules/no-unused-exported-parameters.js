const FUNCTION_TYPES = new Set(['FunctionDeclaration', 'FunctionExpression', 'ArrowFunctionExpression']);

function parameterIdentifiers(pattern, result = []) {
  switch (pattern.type) {
    case 'Identifier':
      if (pattern.name !== 'this') {
        result.push(pattern);
      }
      break;
    case 'RestElement':
      parameterIdentifiers(pattern.argument, result);
      break;
    case 'AssignmentPattern':
      parameterIdentifiers(pattern.left, result);
      break;
    case 'ArrayPattern':
      for (const element of pattern.elements) {
        if (element) {
          parameterIdentifiers(element, result);
        }
      }
      break;
    case 'ObjectPattern':
      for (const property of pattern.properties) {
        parameterIdentifiers(property.type === 'RestElement' ? property.argument : property.value, result);
      }
      break;
    default:
      break;
  }
  return result;
}

function publicClassMember(member) {
  return member.accessibility !== 'private' && member.key?.type !== 'PrivateIdentifier';
}

function callablesFromClass(classNode, moduleScope, visited) {
  if (!classNode?.body?.body) {
    return [];
  }
  return classNode.body.body.flatMap((member) => {
    if (!publicClassMember(member)) {
      return [];
    }
    if (member.type === 'MethodDefinition') {
      return callablesFromExpression(member.value, moduleScope, visited);
    }
    if (member.type === 'PropertyDefinition' || member.type === 'FieldDefinition') {
      return callablesFromExpression(member.value, moduleScope, visited);
    }
    return [];
  });
}

function callablesFromObject(objectNode, moduleScope, visited) {
  return objectNode.properties.flatMap((property) => {
    if (property.type === 'SpreadElement') {
      return callablesFromExpression(property.argument, moduleScope, visited);
    }
    if (property.type !== 'Property') {
      return [];
    }
    return callablesFromExpression(property.value, moduleScope, visited);
  });
}

function callablesFromExpression(node, moduleScope, visited = new Set()) {
  if (!node || visited.has(node)) {
    return [];
  }
  visited.add(node);

  if (FUNCTION_TYPES.has(node.type)) {
    return [node];
  }
  if (node.type === 'ClassDeclaration' || node.type === 'ClassExpression') {
    return callablesFromClass(node, moduleScope, visited);
  }
  if (node.type === 'ObjectExpression') {
    return callablesFromObject(node, moduleScope, visited);
  }
  if (node.type === 'Identifier') {
    const variable = moduleScope.set.get(node.name);
    return (variable?.defs ?? []).flatMap((definition) => {
      const definedNode = definition.node;
      if (definedNode.type === 'VariableDeclarator') {
        return callablesFromExpression(definedNode.init, moduleScope, visited);
      }
      if (definedNode.type === 'FunctionDeclaration' || definedNode.type === 'ClassDeclaration') {
        return callablesFromExpression(definedNode, moduleScope, visited);
      }
      return [];
    });
  }
  return [];
}

function callablesFromDeclaration(declaration, moduleScope) {
  if (!declaration) {
    return [];
  }
  if (declaration.type === 'VariableDeclaration') {
    return declaration.declarations.flatMap(({ init }) => callablesFromExpression(init, moduleScope));
  }
  return callablesFromExpression(declaration, moduleScope);
}

export default {
  meta: {
    type: 'problem',
    docs: {
      description: 'reject unused parameters in exported functions',
    },
    schema: [],
    messages: {
      unused: "Exported parameter '{{name}}' is unused; remove it from the public signature.",
    },
  },
  create(context) {
    const checkedFunctions = new Set();

    function checkFunction(fn) {
      if (!fn?.body || checkedFunctions.has(fn)) {
        return;
      }
      checkedFunctions.add(fn);

      const functionScope = context.sourceCode.getScope(fn);
      for (const param of fn.params) {
        for (const identifier of parameterIdentifiers(param)) {
          const variable = functionScope.set.get(identifier.name);
          if (variable && !variable.references.some((reference) => reference.isRead())) {
            context.report({
              node: identifier,
              messageId: 'unused',
              data: { name: identifier.name },
            });
          }
        }
      }
    }

    function checkDeclaration(declaration, moduleScope) {
      for (const fn of callablesFromDeclaration(declaration, moduleScope)) {
        checkFunction(fn);
      }
    }

    return {
      ExportNamedDeclaration(node) {
        const moduleScope = context.sourceCode.scopeManager.scopes.find((scope) => scope.type === 'module');
        checkDeclaration(node.declaration, moduleScope);
      },
      ExportDefaultDeclaration(node) {
        const moduleScope = context.sourceCode.scopeManager.scopes.find((scope) => scope.type === 'module');
        checkDeclaration(node.declaration, moduleScope);
      },
      'Program:exit'(program) {
        const programScope = context.sourceCode.scopeManager.scopes.find((scope) => scope.type === 'module');
        if (!programScope) {
          return;
        }
        for (const statement of program.body) {
          const isLocalExport = statement.type === 'ExportNamedDeclaration' && statement.source === null;
          const isDefaultIdentifier = statement.type === 'ExportDefaultDeclaration' && statement.declaration.type === 'Identifier';
          if (!isLocalExport && !isDefaultIdentifier) {
            continue;
          }

          const specifiers = isDefaultIdentifier
            ? [{ local: statement.declaration }]
            : statement.specifiers.filter((specifier) => specifier.type === 'ExportSpecifier').map((specifier) => ({ local: specifier.local }));
          for (const specifier of specifiers) {
            for (const fn of callablesFromExpression(specifier.local, programScope)) {
              checkFunction(fn);
            }
          }
        }
      },
    };
  },
};
