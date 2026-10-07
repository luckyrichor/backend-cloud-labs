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

namespace rules {
using Variables = std::unordered_map<std::string, double>;
struct Node {
  std::string op;
  double number{};
  std::unique_ptr<Node> left, right;
  double evaluate(const Variables& vars) const {
    if (op == "number") return number;
    if (op == "variable") {
      auto found = vars.find(name);
      if (found == vars.end() || !std::isfinite(found->second)) throw std::runtime_error("invalid variable");
      return found->second;
    }
    double a = left->evaluate(vars);
    if (op == "neg") return -a;
    if (op == "!") return a == 0;
    if (op == "&&" && a == 0) return 0;
    if (op == "||" && a != 0) return 1;
    double b = right->evaluate(vars), result{};
    if (op == "+") result = a+b;
    else if (op == "-") result = a-b;
    else if (op == "*") result = a*b;
    else if (op == "/") { if (b == 0) throw std::runtime_error("division by zero"); result = a/b; }
    else if (op == "<") result = a<b;
    else if (op == "<=") result = a<=b;
    else if (op == ">") result = a>b;
    else if (op == ">=") result = a>=b;
    else if (op == "==") result = a==b;
    else if (op == "!=") result = a!=b;
    else if (op == "&&" || op == "||") result = b != 0;
    else throw std::runtime_error("invalid operator");
    if (!std::isfinite(result)) throw std::runtime_error("nonfinite result");
    return result;
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
    auto n=std::make_unique<Node>(); n->op=std::move(op); n->left=std::move(a); n->right=std::move(b); return n;
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
