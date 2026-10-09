#pragma once
#include <cmath>
#include <cctype>
#include <cstdlib>
#include <functional>
#include <memory>
#include <stdexcept>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>
#include <span>

namespace rules {
using Variables = std::unordered_map<std::string, double>;
enum class Op { Number, Variable, Negate, Not, Add, Subtract, Multiply, Divide,
                Less, LessEqual, Greater, GreaterEqual, Equal, NotEqual, And, Or };
inline Op parse_op(std::string_view token) {
  static constexpr std::pair<std::string_view, Op> mapping[] = {
    {"number",Op::Number},{"variable",Op::Variable},{"neg",Op::Negate},{"!",Op::Not},
    {"+",Op::Add},{"-",Op::Subtract},{"*",Op::Multiply},{"/",Op::Divide},
    {"<",Op::Less},{"<=",Op::LessEqual},{">",Op::Greater},{">=",Op::GreaterEqual},
    {"==",Op::Equal},{"!=",Op::NotEqual},{"&&",Op::And},{"||",Op::Or}};
  for (auto [text, op] : mapping) if (text == token) return op;
  throw std::runtime_error("invalid operator");
}
struct Node {
  Op op{Op::Number};
  double number{};
  std::unique_ptr<Node> left, right;
  std::size_t slot{static_cast<std::size_t>(-1)};
  template<class Load> double evaluate_by(const Load& load) const {
    if (op == Op::Number) return number;
    if (op == Op::Variable) return load(*this);
    double a = left->evaluate_by(load);
    if (op == Op::Negate) return -a;
    if (op == Op::Not) return a == 0;
    if (op == Op::And && a == 0) return 0;
    if (op == Op::Or && a != 0) return 1;
    double b = right->evaluate_by(load), result{};
    switch (op) {
      case Op::Add: result = a+b; break;
      case Op::Subtract: result = a-b; break;
      case Op::Multiply: result = a*b; break;
      case Op::Divide:
        if (b == 0) throw std::runtime_error("division by zero");
        result = a/b; break;
      case Op::Less: result = a<b; break;
      case Op::LessEqual: result = a<=b; break;
      case Op::Greater: result = a>b; break;
      case Op::GreaterEqual: result = a>=b; break;
      case Op::Equal: result = a==b; break;
      case Op::NotEqual: result = a!=b; break;
      case Op::And: case Op::Or: result = b != 0; break;
      default: throw std::runtime_error("invalid operator");
    }
    if (!std::isfinite(result)) throw std::runtime_error("nonfinite result");
    return result;
  }
  double evaluate(const Variables& vars) const {
    return evaluate_by([&](const Node& n) {
      auto it=vars.find(n.name);
      if(it==vars.end() || !std::isfinite(it->second)) throw std::runtime_error("invalid variable");
      return it->second;
    });
  }
  // Bind once before sharing the AST. Evaluation afterwards is immutable.
  void bind_variables(std::span<const std::string> schema) {
    if(schema.size()>256) throw std::runtime_error("schema limit");
    std::unordered_map<std::string,std::size_t> indices;
    for(std::size_t i=0;i<schema.size();++i)
      if(schema[i].empty() || !indices.emplace(schema[i],i).second) throw std::runtime_error("invalid schema");
    std::vector<std::pair<Node*,std::size_t>> assignments;
    std::function<void(Node&)> collect=[&](Node& n) {
      if(n.op==Op::Variable) {
        auto it=indices.find(n.name);if(it==indices.end()) throw std::runtime_error("missing schema variable");
        assignments.emplace_back(&n,it->second);
      }
      if(n.left) collect(*n.left);
      if(n.right) collect(*n.right);
    };
    collect(*this); // Validation completes before mutating any binding.
    for(auto [node,index]:assignments) node->slot=index;
  }
  double evaluate(std::span<const double> values) const {
    return evaluate_by([&](const Node& n) {
      if(n.slot>=values.size() || !std::isfinite(values[n.slot])) throw std::runtime_error("invalid variable slot");
      return values[n.slot];
    });
  }
  // Sequential batch APIs reuse this immutable compiled expression. Error policy:
  // fail fast with the row index; no partial result is returned, input is untouched.
  std::vector<double> evaluate_batch(std::span<const Variables> documents) const {
    std::vector<double> results;
    results.reserve(documents.size());
    for (std::size_t i=0; i<documents.size(); ++i) {
      try { results.push_back(evaluate(documents[i])); }
      catch (const std::runtime_error& e) {
        throw std::runtime_error("document " + std::to_string(i) + ": " + e.what());
      }
    }
    return results;
  }
  std::vector<std::size_t> filter_batch(std::span<const Variables> documents) const {
    std::vector<std::size_t> matches;
    matches.reserve(documents.size());
    for (std::size_t i=0; i<documents.size(); ++i) {
      try { if (evaluate(documents[i]) != 0) matches.push_back(i); }
      catch (const std::runtime_error& e) {
        throw std::runtime_error("document " + std::to_string(i) + ": " + e.what());
      }
    }
    return matches;
  }
  std::string name;
};
class Parser {
  std::string text;
  std::size_t pos{}, count{};
  void space() { while(pos < text.size() && std::isspace(static_cast<unsigned char>(text[pos]))) ++pos; }
  bool eat(std::string_view token) { space(); if(text.compare(pos, token.size(), token)==0) { pos+=token.size(); return true; } return false; }
  std::unique_ptr<Node> node(std::string op, std::unique_ptr<Node> a={}, std::unique_ptr<Node> b={}) {
    if (++count > 256) throw std::runtime_error("node limit");
    auto n=std::make_unique<Node>(); n->op=parse_op(op); n->left=std::move(a); n->right=std::move(b); return n;
  }
  std::unique_ptr<Node> expression(int level, int depth) {
    if(depth > 64) throw std::runtime_error("depth limit");
    static const std::vector<std::vector<std::string>> operators={{"||"},{"&&"},{"==","!="},{"<=",">=","<",">"},{"+","-"},{"*","/"}};
    if(level == 6) {
      if(eat("-")) return node("neg", expression(6,depth+1));
      if(eat("!")) return node("!", expression(6,depth+1));
      if(eat("(")) { auto n=expression(0,depth+1); if(!eat(")")) throw std::runtime_error("missing parenthesis"); return n; }
      space();
      if(pos==text.size()) throw std::runtime_error("missing operand");
      unsigned char c=text[pos];
      if(std::isdigit(c) || c=='.') {
        char* end=nullptr; const char* start=text.c_str()+pos;
        double value=std::strtod(start,&end);
        if(end==start || !std::isfinite(value)) throw std::runtime_error("invalid number");
        pos=static_cast<std::size_t>(end-text.c_str()); auto n=node("number"); n->number=value; return n;
      }
      if(std::isalpha(c) || c=='_') {
        auto start=pos++; while(pos<text.size() && (std::isalnum(static_cast<unsigned char>(text[pos])) || text[pos]=='_')) ++pos;
        auto n=node("variable"); n->name=text.substr(start,pos-start); return n;
      }
      throw std::runtime_error("invalid token");
    }
    auto a=expression(level+1,depth);
    while(true) {
      bool matched=false;
      for(const auto& op:operators[level]) if(eat(op)) { a=node(op,std::move(a),expression(level+1,depth)); matched=true; break; }
      if(!matched) return a;
    }
  }
public:
  explicit Parser(std::string source):text(std::move(source)) { if(text.size()>4096) throw std::runtime_error("input limit"); }
  std::unique_ptr<Node> compile() { auto n=expression(0,0); space(); if(pos!=text.size()) throw std::runtime_error("trailing token"); return n; }
};
}
